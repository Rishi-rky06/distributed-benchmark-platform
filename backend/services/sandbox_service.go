package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// ContainerRuntime abstracts the OCI runtime (runc vs gVisor).
type ContainerRuntime interface {
	RuntimeName() string
	ApplyHostConfig(hc *container.HostConfig)
}

type RuncRuntime struct{}

func (r *RuncRuntime) RuntimeName() string                     { return "runc" }
func (r *RuncRuntime) ApplyHostConfig(hc *container.HostConfig) {}

type GVisorRuntime struct{}

func (g *GVisorRuntime) RuntimeName() string                     { return "runsc" }
func (g *GVisorRuntime) ApplyHostConfig(hc *container.HostConfig) { hc.Runtime = "runsc" }

// SandboxService manages contestant container lifecycle.
type SandboxService struct {
	cfg           *config.Config
	log           *utils.Logger
	docker        *client.Client
	runtime       ContainerRuntime
	dockerNetwork string // Docker network to attach submission containers to
}

func NewSandboxService(cfg *config.Config, log *utils.Logger) (*SandboxService, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker ping: %w", err)
	}

	var rt ContainerRuntime = &RuncRuntime{}
	if cfg.SandboxNetworkMode == "gvisor" {
		rt = &GVisorRuntime{}
	}

	// Auto-detect the Docker network this container is on so submission
	// containers can be attached to the same network for direct connectivity.
	dockerNetwork := detectOwnNetwork(ctx, cli)

	log.Infow("sandbox service initialized", "runtime", rt.RuntimeName(), "network", dockerNetwork)
	return &SandboxService{cfg: cfg, log: log, docker: cli, runtime: rt, dockerNetwork: dockerNetwork}, nil
}

// detectOwnNetwork returns the first Docker network the current container is
// attached to by inspecting the container whose ID matches our hostname.
func detectOwnNetwork(ctx context.Context, cli *client.Client) string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	info, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		return ""
	}
	for netName := range info.NetworkSettings.Networks {
		return netName
	}
	return ""
}

func (s *SandboxService) Close() {
	if s.docker != nil {
		s.docker.Close()
	}
}

// ContainerInfo holds launch results.
type ContainerInfo struct {
	ContainerID string
	Host        string
	Port        int
}

// BuildImage builds a Docker image for the submission.
func (s *SandboxService) BuildImage(ctx context.Context, subID, language, subDir string) (string, error) {
	tag := fmt.Sprintf("bench-sub-%s:latest", subID[:8])

	// Extract zip archive if the submission was uploaded as one. This populates
	// subDir with the actual source files (and the user's own Dockerfile if present).
	if err := extractSubmissionZip(subDir); err != nil {
		return "", fmt.Errorf("extract submission: %w", err)
	}

	// Use the submission's own Dockerfile when provided (e.g. C++ with CMake,
	// Python with custom deps). Only fall back to a generated one when absent.
	dockerfilePath := filepath.Join(subDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		content := dockerfileFor(language)
		if content == "" {
			return "", fmt.Errorf("unsupported language: %s", language)
		}
		if err := os.WriteFile(dockerfilePath, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write dockerfile: %w", err)
		}
	}

	buildCtx, err := createTarContext(subDir)
	if err != nil {
		return "", fmt.Errorf("tar context: %w", err)
	}

	resp, err := s.docker.ImageBuild(ctx, buildCtx, types.ImageBuildOptions{
		Tags: []string{tag}, Dockerfile: "Dockerfile", Remove: true, ForceRemove: true,
	})
	if err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}
	defer resp.Body.Close()
	if err := jsonmessage.DisplayJSONMessagesStream(resp.Body, io.Discard, 0, false, nil); err != nil {
		return "", fmt.Errorf("docker build stream: %w", err)
	}

	s.log.Infow("image built", "tag", tag, "submission_id", subID)
	return tag, nil
}

// LaunchContainer starts a sandboxed container.
func (s *SandboxService) LaunchContainer(ctx context.Context, imageTag, subID string) (*ContainerInfo, error) {
	name := fmt.Sprintf("bench-sub-%s", subID[:8])
	cpuNano, memBytes := s.parseLimits()

	ccfg := &container.Config{
		Image:  imageTag,
		Labels: map[string]string{"bench.submission_id": subID, "bench.managed": "true"},
	}
	hcfg := &container.HostConfig{
		Resources:      container.Resources{NanoCPUs: cpuNano, Memory: memBytes},
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/tmp": "rw,noexec,nosuid,size=64m"},
		SecurityOpt:    []string{"no-new-privileges"},
	}
	s.runtime.ApplyHostConfig(hcfg)

	// Attach to the same Docker network as the backend for direct connectivity.
	var netCfg *network.NetworkingConfig
	if s.dockerNetwork != "" {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				s.dockerNetwork: {},
			},
		}
	}

	resp, err := s.docker.ContainerCreate(ctx, ccfg, hcfg, netCfg, nil, name)
	if err != nil {
		return nil, fmt.Errorf("create: %w", err)
	}
	if err := s.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = s.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start: %w", err)
	}

	inspect, err := s.docker.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}

	// Use the container's internal IP on our shared network.
	host := ""
	if s.dockerNetwork != "" {
		if ep, ok := inspect.NetworkSettings.Networks[s.dockerNetwork]; ok {
			host = ep.IPAddress
		}
	}
	// Fallback: use container name (DNS works on custom bridge networks).
	if host == "" {
		host = name
	}

	s.log.Infow("container launched", "id", resp.ID[:12], "host", host)
	return &ContainerInfo{ContainerID: resp.ID, Host: host, Port: 8080}, nil
}

// WaitForHealthy polls the container endpoint.
func (s *SandboxService) WaitForHealthy(ctx context.Context, host string, port int, timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	url := fmt.Sprintf("http://%s:%d/health", host, port)
	httpCli := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("health check timed out after %s", timeout)
		case <-tick.C:
			if resp, err := httpCli.Get(url); err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					return nil
				}
			}
		}
	}
}

// TearDown stops and removes a container + image.
func (s *SandboxService) TearDown(ctx context.Context, containerID, imageTag string) error {
	t := 10
	_ = s.docker.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &t})
	_ = s.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if imageTag != "" {
		_, _ = s.docker.ImageRemove(ctx, imageTag, imagetypes.RemoveOptions{Force: true, PruneChildren: true})
	}
	s.log.Infow("container torn down", "id", containerID[:12])
	return nil
}

// GetContainerLogs retrieves tail logs.
func (s *SandboxService) GetContainerLogs(ctx context.Context, cid string, lines int) (string, error) {
	r, err := s.docker.ContainerLogs(ctx, cid, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: fmt.Sprintf("%d", lines),
	})
	if err != nil {
		return "", err
	}
	defer r.Close()
	var stdout, stderr bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdout, &stderr, r)
	return stdout.String() + stderr.String(), nil
}

func (s *SandboxService) parseLimits() (int64, int64) {
	var cpuF float64
	fmt.Sscanf(s.cfg.SandboxCPULimit, "%f", &cpuF)
	nano := int64(cpuF * 1e9)
	mem := strings.ToLower(s.cfg.SandboxMemoryLimit)
	var v int64
	if strings.HasSuffix(mem, "g") {
		fmt.Sscanf(mem, "%dg", &v)
		return nano, v * 1024 * 1024 * 1024
	}
	if strings.HasSuffix(mem, "m") {
		fmt.Sscanf(mem, "%dm", &v)
		return nano, v * 1024 * 1024
	}
	fmt.Sscanf(mem, "%d", &v)
	return nano, v
}

func dockerfileFor(lang string) string {
	m := map[string]string{
		"go":   "FROM golang:1.22-alpine AS builder\nWORKDIR /build\nCOPY . .\nRUN go mod init submission 2>/dev/null || true\nRUN CGO_ENABLED=0 GOOS=linux go build -ldflags=\"-s -w\" -o /app/server .\nFROM alpine:3.19\nRUN apk --no-cache add ca-certificates\nCOPY --from=builder /app/server /app/server\nEXPOSE 8080\nENTRYPOINT [\"/app/server\"]\n",
		"cpp":  "FROM gcc:13 AS builder\nWORKDIR /build\nCOPY . .\nRUN g++ -O2 -std=c++20 -o /app/server *.cpp -lpthread\nFROM debian:bookworm-slim\nCOPY --from=builder /app/server /app/server\nEXPOSE 8080\nENTRYPOINT [\"/app/server\"]\n",
		"rust": "FROM rust:1.77-slim AS builder\nWORKDIR /build\nCOPY . .\nRUN cargo init --name submission 2>/dev/null || true\nRUN cargo build --release\nRUN cp target/release/submission /app/server\nFROM debian:bookworm-slim\nCOPY --from=builder /app/server /app/server\nEXPOSE 8080\nENTRYPOINT [\"/app/server\"]\n",
		"python": "FROM python:3.12-slim\nENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1\nWORKDIR /app\nCOPY . .\nRUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true\nEXPOSE 8080\nCMD [\"python\", \"main.py\"]\n",
	}
	return m[strings.ToLower(lang)]
}
