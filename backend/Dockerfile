# ── Development target ──────────────────────────────────────────────
FROM golang:1.22-alpine AS development

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd/main.go

EXPOSE 8080
ENTRYPOINT ["/app/server"]


# ── Production target ──────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/server ./cmd/main.go


FROM alpine:3.19 AS production

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /build/server /app/server
COPY --from=builder /build/migrations /app/migrations

# Copy frontend static files
COPY --from=builder /build/../frontend /app/frontend 2>/dev/null || true

EXPOSE 8080
ENTRYPOINT ["/app/server"]
