# Architecture Blueprint — IICPC Distributed Benchmark Platform

## System Overview

A distributed platform for evaluating contestant-submitted trading infrastructure under simulated market stress. The system follows a **submission → queue → build → deploy → load-test → score → rank** pipeline.

```
┌──────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│ Frontend │────▶│  Gin API     │────▶│  Redis Queue  │────▶│  Benchmark   │
│ (Static) │     │  (REST+WS)   │     │  (FIFO)       │     │  Worker      │
└──────────┘     └──────────────┘     └──────────────┘     └──────┬───────┘
                        │                                         │
                        ▼                                         ▼
                 ┌──────────────┐                          ┌──────────────┐
                 │  PostgreSQL  │◀─────────────────────────│   Sandbox    │
                 │  (Metrics,   │                          │   (Docker)   │
                 │  Leaderboard)│                          └──────┬───────┘
                 └──────────────┘                                 │
                        ▲                                         ▼
                        │                                  ┌──────────────┐
                 ┌──────────────┐                          │  Bot Fleet   │
                 │  Redis       │◀─────────────────────────│  (Goroutine  │
                 │  Pub/Sub     │     LiveMetricEvents     │   Workers)   │
                 └──────┬───────┘                          └──────┬───────┘
                        │                                         │
                        ▼                                         ▼
                 ┌──────────────┐                          ┌──────────────┐
                 │  WebSocket   │                          │  Telemetry   │
                 │  Broadcast   │                          │  Ingester    │
                 └──────────────┘                          │ (HdrHistogram)│
                                                           └──────────────┘
```

## Component Details

### 1. API Layer (Gin Router)

- **Framework**: Go 1.22 + Gin
- **Middleware**: Structured logging (zap), panic recovery, request IDs, CORS, security headers, body limits
- **Endpoints**:
  - `POST /api/v1/submissions` — multipart file upload
  - `GET /api/v1/submissions` — paginated list
  - `GET/DELETE /api/v1/submissions/:id` — get/delete
  - `POST /api/v1/submissions/:id/run` — trigger benchmark
  - `GET /api/v1/leaderboard` — ranked snapshot
  - `GET /api/v1/leaderboard/ws` — WebSocket live stream
  - `GET /api/v1/leaderboard/runs/:runID/metrics` — time-series metrics

### 2. Submission & Sandboxing Engine

- **Docker SDK** (`github.com/docker/docker/client`) — no CLI shelling
- **Docker socket mount** for container management
- **Runtime abstraction**: `ContainerRuntime` interface with `runc` (default) and `gVisor` implementations
- **Security hardening**:
  - CPU pinning via `NanoCPUs`
  - Hard memory limits
  - Read-only root filesystem
  - `/tmp` as noexec tmpfs (64MB)
  - `no-new-privileges` security option
- **Multi-language support**: Auto-generated Dockerfiles for Go, C++, Rust

### 3. Distributed Load Generator (Bot Fleet)

- **Architecture**: Goroutine-per-worker model with linear ramp-up
- **Config**: min/max workers, ramp duration, orders/sec/worker
- **Order generation**: Ornstein-Uhlenbeck stochastic process for realistic price movements
- **Order mix**: 60% limit, 30% market, 10% cancel
- **Protocol adapters** (pluggable `ProtocolAdapter` interface):
  - `RESTAdapter` — HTTP POST with connection pooling
  - `WebSocketAdapter` — persistent WS connections, serialized writes
  - FIX — placeholder for future implementation
- **Telemetry**: Non-blocking sample channel (100K buffer)

### 4. Telemetry & Validation Ingester

- Drains bot fleet's `LatencySample` channel
- **HdrHistogram** for percentile computation (p50/p90/p99)
- Flushes at configurable interval (default 500ms):
  - `MetricSnapshot` rows → PostgreSQL
  - `LiveMetricEvent` → Redis pub/sub → WebSocket clients
- Tracks: latency, TPS, orders sent/acked, fill errors, priority errors

### 5. Scoring Engine

- **Normalization** (log-scale):
  - Latency: 100 at ≤1ms p99, 0 at ≥100ms p99
  - Throughput: 100 at ≥100K TPS, 0 at ≤100 TPS
  - Correctness: direct percentage
  - Stability: uptime percentage
- **Composite**: weighted sum (30% latency, 30% throughput, 30% correctness, 10% stability)
- **Leaderboard**: upsert-on-better-score with `leaderboard_ranked` SQL view

### 6. Data Stores

| Store | Purpose | Key Tables |
|-------|---------|------------|
| PostgreSQL 16 | Persistent state | `submissions`, `benchmark_runs`, `metric_snapshots`, `metric_aggregates`, `leaderboard` |
| Redis 7 | Queue + pub/sub | `queue:benchmark_runs` (FIFO), `queue:dead_letter`, `telemetry:stream` (pub/sub) |

### 7. Frontend

- **Technology**: Static HTML/CSS/JS (no build tools)
- **Pages**: Dashboard, Submit, Leaderboard
- **Served by**: Gin static file middleware

## Inter-Service Communication

| Path | Protocol | Notes |
|------|----------|-------|
| Frontend → API | HTTP REST | Standard JSON envelope |
| API → Worker | Redis LPUSH/BRPOP | FIFO work queue |
| Worker → Sandbox | Docker SDK | gRPC-based Docker API |
| Bot Fleet → Submission | REST / WebSocket | Configurable per run |
| Telemetry → Frontend | Redis Pub/Sub → WebSocket | Real-time streaming |

## Isolation Strategy

```
Host OS
  └── Docker Socket (mounted)
       └── Backend Container
            └── Docker SDK Client
                 └── Sandbox Container (contestant code)
                      ├── ReadOnly rootfs
                      ├── NanoCPU limits
                      ├── Memory hard limit
                      ├── noexec /tmp
                      └── no-new-privileges
```

## Deployment (Docker Compose)

```yaml
services:
  postgres:   # PostgreSQL 16 Alpine
  redis:      # Redis 7 Alpine with AOF
  backend:    # Go binary (multi-stage build)
  frontend:   # Nginx serving static files
```

All services connected via `bench-net` bridge network. Health checks configured for dependency ordering.
