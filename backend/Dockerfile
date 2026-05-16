# ── Development target ──────────────────────────────────────────────
FROM golang:1.22-alpine AS development

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Sync go.sum in case it is stale (uses the already-cached modules above)
RUN go mod tidy

# Build binary to /usr/local/bin so the volume mount at /app cannot shadow it
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /usr/local/bin/server ./cmd/main.go

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]


# ── Production target ──────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/server ./cmd/main.go


FROM alpine:3.19 AS production

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /build/server /app/server
COPY --from=builder /build/migrations /app/migrations

# Note: Frontend static files are served by the nginx container, not the backend

EXPOSE 8080
ENTRYPOINT ["/app/server"]
