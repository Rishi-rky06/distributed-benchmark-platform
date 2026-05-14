package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the platform.
type Config struct {
	// ── Application ───────────────────────────────────────────────────────────
	AppEnv    string
	SecretKey string

	// ── HTTP Server ───────────────────────────────────────────────────────────
	BackendPort  string
	LogLevel     string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxUploadMB  int64

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	PostgresHost            string
	PostgresPort            string
	PostgresDB              string
	PostgresUser            string
	PostgresPassword        string
	PostgresSSLMode         string
	PostgresMaxOpenConns    int
	PostgresMaxIdleConns    int
	PostgresConnMaxLifetime time.Duration

	// ── Redis ─────────────────────────────────────────────────────────────────
	RedisHost             string
	RedisPort             string
	RedisPassword         string
	RedisDB               int
	RedisTelemetryChannel string
	RedisLeaderboardKey   string

	// ── Sandbox ───────────────────────────────────────────────────────────────
	SandboxCPULimit       string
	SandboxMemoryLimit    string
	SandboxTimeoutSeconds int
	SandboxNetworkMode    string
	SubmissionsDir        string

	// ── Bot Fleet ─────────────────────────────────────────────────────────────
	BotFleetMinWorkers    int
	BotFleetMaxWorkers    int
	BotRampDuration       time.Duration
	BotProtocol           string
	BotOrderRatePerWorker int

	// ── Telemetry & Scoring ───────────────────────────────────────────────────
	TelemetryFlushInterval time.Duration
	BenchmarkDuration      time.Duration
	ScoreWeightLatency     float64
	ScoreWeightThroughput  float64
	ScoreWeightCorrectness float64
	ScoreWeightStability   float64

	// ── JWT ───────────────────────────────────────────────────────────────────
	JWTSecret string
	JWTExpiry time.Duration
}

// Load reads environment variables (loading .env first in non-prod envs)
// and returns a validated Config.
func Load() (*Config, error) {
	appEnv := getEnv("APP_ENV", "development")
	if appEnv != "production" {
		_ = godotenv.Load() // non-fatal — may not exist in CI
	}

	cfg := &Config{
		AppEnv:    appEnv,
		SecretKey: requireEnv("APP_SECRET_KEY"),

		BackendPort: getEnv("BACKEND_PORT", "8080"),
		LogLevel:    getEnv("BACKEND_LOG_LEVEL", "info"),
		MaxUploadMB: getEnvInt64("BACKEND_MAX_UPLOAD_MB", 256),

		PostgresHost:         getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:         getEnv("POSTGRES_PORT", "5432"),
		PostgresDB:           requireEnv("POSTGRES_DB"),
		PostgresUser:         requireEnv("POSTGRES_USER"),
		PostgresPassword:     requireEnv("POSTGRES_PASSWORD"),
		PostgresSSLMode:      getEnv("POSTGRES_SSLMODE", "disable"),
		PostgresMaxOpenConns: getEnvInt("POSTGRES_MAX_OPEN_CONNS", 25),
		PostgresMaxIdleConns: getEnvInt("POSTGRES_MAX_IDLE_CONNS", 5),

		RedisHost:             getEnv("REDIS_HOST", "localhost"),
		RedisPort:             getEnv("REDIS_PORT", "6379"),
		RedisPassword:         getEnv("REDIS_PASSWORD", ""),
		RedisDB:               getEnvInt("REDIS_DB", 0),
		RedisTelemetryChannel: getEnv("REDIS_TELEMETRY_CHANNEL", "telemetry:stream"),
		RedisLeaderboardKey:   getEnv("REDIS_LEADERBOARD_KEY", "leaderboard:global"),

		SandboxCPULimit:       getEnv("SANDBOX_CPU_LIMIT", "1.0"),
		SandboxMemoryLimit:    getEnv("SANDBOX_MEMORY_LIMIT", "512m"),
		SandboxTimeoutSeconds: getEnvInt("SANDBOX_TIMEOUT_SECONDS", 300),
		SandboxNetworkMode:    getEnv("SANDBOX_NETWORK_MODE", "isolated"),
		SubmissionsDir:        getEnv("SUBMISSIONS_DIR", "./submissions"),

		BotFleetMinWorkers:    getEnvInt("BOT_FLEET_MIN_WORKERS", 100),
		BotFleetMaxWorkers:    getEnvInt("BOT_FLEET_MAX_WORKERS", 5000),
		BotProtocol:           getEnv("BOT_PROTOCOL", "websocket"),
		BotOrderRatePerWorker: getEnvInt("BOT_ORDER_RATE_PER_WORKER", 50),

		ScoreWeightLatency:     getEnvFloat("SCORE_WEIGHT_LATENCY", 0.30),
		ScoreWeightThroughput:  getEnvFloat("SCORE_WEIGHT_THROUGHPUT", 0.30),
		ScoreWeightCorrectness: getEnvFloat("SCORE_WEIGHT_CORRECTNESS", 0.30),
		ScoreWeightStability:   getEnvFloat("SCORE_WEIGHT_STABILITY", 0.10),

		JWTSecret: requireEnv("JWT_SECRET"),
	}

	var err error
	if cfg.ReadTimeout, err = time.ParseDuration(getEnv("BACKEND_READ_TIMEOUT", "30s")); err != nil {
		return nil, fmt.Errorf("BACKEND_READ_TIMEOUT: %w", err)
	}
	if cfg.WriteTimeout, err = time.ParseDuration(getEnv("BACKEND_WRITE_TIMEOUT", "30s")); err != nil {
		return nil, fmt.Errorf("BACKEND_WRITE_TIMEOUT: %w", err)
	}
	if cfg.PostgresConnMaxLifetime, err = time.ParseDuration(getEnv("POSTGRES_CONN_MAX_LIFETIME", "5m")); err != nil {
		return nil, fmt.Errorf("POSTGRES_CONN_MAX_LIFETIME: %w", err)
	}
	if cfg.BotRampDuration, err = time.ParseDuration(getEnv("BOT_RAMP_DURATION", "30s")); err != nil {
		return nil, fmt.Errorf("BOT_RAMP_DURATION: %w", err)
	}
	if cfg.TelemetryFlushInterval, err = time.ParseDuration(getEnv("TELEMETRY_FLUSH_INTERVAL", "500ms")); err != nil {
		return nil, fmt.Errorf("TELEMETRY_FLUSH_INTERVAL: %w", err)
	}
	if cfg.BenchmarkDuration, err = time.ParseDuration(getEnv("BENCHMARK_DURATION", "60s")); err != nil {
		return nil, fmt.Errorf("BENCHMARK_DURATION: %w", err)
	}
	if cfg.JWTExpiry, err = time.ParseDuration(getEnv("JWT_EXPIRY", "24h")); err != nil {
		return nil, fmt.Errorf("JWT_EXPIRY: %w", err)
	}

	return cfg, nil
}

// DSN returns the PostgreSQL connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
		c.PostgresHost, c.PostgresPort, c.PostgresDB,
		c.PostgresUser, c.PostgresPassword, c.PostgresSSLMode,
	)
}

// RedisAddr returns host:port for Redis.
func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%s", c.RedisHost, c.RedisPort)
}

// IsProd returns true in production mode.
func (c *Config) IsProd() bool { return c.AppEnv == "production" }

// ── env helpers ──────────────────────────────────────────────────────────────

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
