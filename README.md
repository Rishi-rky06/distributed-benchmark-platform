# Distributed Benchmarking Platform — IICPC Summer Hackathon 2026

A high-performance distributed system for evaluating contestant-submitted trading infrastructure under extreme load conditions.

---

## Overview

This platform provides an end-to-end pipeline for:

1. **Secure submission ingestion** — Contestants upload source code (C++, Rust, Go); the platform containerizes it in an isolated sandbox with strict CPU/memory limits.
2. **Distributed load generation** — A scalable bot fleet spawns thousands of concurrent traders, firing FIX/REST/WebSocket order traffic at the contestant's endpoints.
3. **Telemetry & validation** — Real-time measurement of p50/p90/p99 latency, max TPS, and correctness (price-time priority, fill accuracy).
4. **Live leaderboard** — A streaming frontend dashboard ranks submissions dynamically by composite score.

---

## Tech Stack

| Layer        | Technology                          |
|--------------|-------------------------------------|
| Backend      | Go 1.22+ · Gin · gRPC               |
| Frontend     | React 18 · Vite · TypeScript        |
| Primary DB   | PostgreSQL 16                       |
| Cache / PubSub | Redis 7                           |
| Containerization | Docker · Docker Compose         |
| IaC (Phase 2) | Kubernetes · Terraform             |

---

## Repository Structure

```
distributed-benchmark-platform/
├── backend/
│   ├── cmd/
│   │   └── server/          # Application entrypoint (main.go)
│   ├── internal/
│   │   ├── api/             # HTTP handlers & route definitions
│   │   ├── config/          # Environment & configuration loading
│   │   ├── models/          # Domain models & DB schemas
│   │   └── services/        # Business logic (submission, bot mgr, telemetry)
│   ├── pkg/                 # Shared/exportable packages
│   ├── migrations/          # SQL migration files
│   ├── Dockerfile
│   └── go.mod
├── frontend/
│   ├── src/
│   │   ├── components/      # Reusable UI components
│   │   ├── pages/           # Route-level page components
│   │   ├── hooks/           # Custom React hooks
│   │   ├── store/           # Global state (Zustand / Redux)
│   │   └── utils/           # Helpers, API clients
│   ├── public/
│   ├── Dockerfile
│   ├── index.html
│   └── vite.config.ts
├── sample-trading-engine/   # Reference implementation for testing
│   ├── src/
│   └── config/
├── docker-compose.yml
├── .env.example
└── README.md
```

---

## Quick Start

### Prerequisites

- Docker 24+ and Docker Compose v2
- Go 1.22+ (for local backend development)
- Node.js 20+ (for local frontend development)

### 1. Clone & Configure

```bash
git clone https://github.com/your-org/distributed-benchmark-platform.git
cd distributed-benchmark-platform
cp .env.example .env
# Edit .env with your values
```

### 2. Spin Up All Services

```bash
docker compose up --build
```

| Service    | Port  | Description              |
|------------|-------|--------------------------|
| Backend    | 8080  | Go/Gin REST + WebSocket  |
| Frontend   | 5173  | React/Vite dev server    |
| PostgreSQL | 5432  | Primary relational DB    |
| Redis      | 6379  | Cache & pub/sub broker   |

### 3. Verify Health

```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"0.1.0"}
```

---

## Development

### Backend (Go)

```bash
cd backend
go mod download
go run cmd/server/main.go
```

### Frontend (React + Vite)

```bash
cd frontend
npm install
npm run dev
```

### Database Migrations

```bash
# Migrations run automatically on backend startup in development.
# For manual runs:
docker compose exec backend ./migrate up
```

---

## Architecture Notes

### Sandboxing Strategy
Contestant submissions are containerized using Docker with:
- CPU pinning via `--cpuset-cpus`
- Hard memory limits (`--memory`, `--memory-swap`)
- No network access by default; only the benchmark bot fleet can reach the container's exposed ports
- Read-only root filesystem; ephemeral writable layer dropped post-test

### Load Generation
The bot fleet is designed as horizontally scalable workers:
- Each worker goroutine manages a pool of simulated market participants
- Orders are generated stochastically following configurable market profiles (HFT, market maker, noise trader)
- Supports REST, WebSocket, and FIX protocol adapters

### Telemetry Pipeline
```
Bot Fleet → gRPC stream → Telemetry Ingester → Redis PubSub → WebSocket → Frontend
                                              ↓
                                         PostgreSQL (TimescaleDB extension recommended)
```

---

## Scoring Model

| Metric          | Weight | Description                               |
|-----------------|--------|-------------------------------------------|
| p99 Latency     | 30%    | Tail latency under peak load              |
| Max TPS         | 30%    | Throughput before first degradation       |
| Correctness     | 30%    | Fill accuracy & price-time priority       |
| Stability       | 10%    | Uptime during sustained 60s stress window |

---

## Contributing

1. Fork the repo and create a feature branch (`git checkout -b feat/your-feature`)
2. Commit with conventional commits (`feat:`, `fix:`, `chore:`)
3. Open a PR against `main` — CI must pass

---

## License

MIT — see [LICENSE](LICENSE)
