package config

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
)

// ── Type aliases exposed to the rest of the app ──────────────────────────────

// DB is the application-wide PostgreSQL handle.
type DB = sqlx.DB

// RedisClient is the application-wide Redis handle.
type RedisClient = redis.Client

// ── PostgreSQL ────────────────────────────────────────────────────────────────

// NewPostgres opens a connection pool, applies pool settings, and pings.
func NewPostgres(cfg *Config) (*DB, error) {
	db, err := sqlx.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("sqlx.Open: %w", err)
	}

	db.SetMaxOpenConns(cfg.PostgresMaxOpenConns)
	db.SetMaxIdleConns(cfg.PostgresMaxIdleConns)
	db.SetConnMaxLifetime(cfg.PostgresConnMaxLifetime)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}

	return db, nil
}

// ── Redis ─────────────────────────────────────────────────────────────────────

// NewRedis creates a go-redis client and verifies connectivity with a PING.
func NewRedis(cfg *Config) (*RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr(),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return rdb, nil
}

// ── Migrations ────────────────────────────────────────────────────────────────

// RunMigrations applies all pending SQL migrations in ./migrations using goose.
// Safe to call on every startup — goose tracks applied versions.
func RunMigrations(db *DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	if err := goose.Up(db.DB, "./migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	return nil
}
