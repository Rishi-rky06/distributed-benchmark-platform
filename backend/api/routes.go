package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// NewRouter builds and returns the fully wired Gin engine.
// All middleware is applied here; handlers are registered per-group.
func NewRouter(
	cfg *config.Config,
	log *utils.Logger,
	db *config.DB,
	rdb *config.RedisClient,
	queue *services.QueueService,
) http.Handler {
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New() // bare engine — no default logger/recovery (we add our own)

	// ── Global middleware ──────────────────────────────────────────────────────
	r.Use(zapLogger(log))       // structured request logging
	r.Use(recovery(log))        // panic → 500, never crash the process
	r.Use(requestID())          // injects X-Request-ID header
	r.Use(corsHeaders(cfg))     // CORS for frontend origin
	r.Use(secureHeaders())      // basic security headers
	r.Use(bodyLimit(cfg.MaxUploadMB)) // prevent oversized payloads outside upload route

	// ── Instantiate handlers ───────────────────────────────────────────────────
	health      := NewHealthHandler(cfg, db, rdb)
	submission  := NewSubmissionHandler(cfg, log, db, rdb, queue)
	leaderboard := NewLeaderboardHandler(cfg, log, db, rdb)

	// ── Readiness / liveness (no auth) ────────────────────────────────────────
	r.GET("/health",   health.Health)
	r.GET("/readiness", health.Readiness)
	r.GET("/livez",    health.Liveness)

	// ── API v1 ─────────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")

	// ── Submissions ────────────────────────────────────────────────────────────
	// POST   /api/v1/submissions          — upload a trading engine
	// GET    /api/v1/submissions          — list all submissions (paginated)
	// GET    /api/v1/submissions/:id      — get one submission
	// DELETE /api/v1/submissions/:id      — cancel / remove a submission
	// POST   /api/v1/submissions/:id/run  — trigger a benchmark run
	// GET    /api/v1/submissions/:id/runs — list runs for a submission
	subs := v1.Group("/submissions")
	{
		subs.POST("",          submission.Create)
		subs.GET("",           submission.List)
		subs.GET("/:id",       submission.Get)
		subs.DELETE("/:id",    submission.Delete)
		subs.POST("/:id/run",  submission.TriggerRun)
		subs.GET("/:id/runs",  submission.ListRuns)
	}

	// ── Leaderboard ────────────────────────────────────────────────────────────
	// GET /api/v1/leaderboard             — current ranked snapshot
	// GET /api/v1/leaderboard/ws          — WebSocket stream of live updates
	// GET /api/v1/leaderboard/runs/:runID/metrics — per-run metric snapshots
	lb := v1.Group("/leaderboard")
	{
		lb.GET("",                      leaderboard.Snapshot)
		lb.GET("/ws",                   leaderboard.Stream)   // upgrades to WS
		lb.GET("/runs/:runID/metrics",  leaderboard.RunMetrics)
	}

	// ── Static frontend ────────────────────────────────────────────────────────
	r.Static("/static", "./frontend")
	r.GET("/", func(c *gin.Context) {
		c.File("./frontend/index.html")
	})

	// ── 404 handler ────────────────────────────────────────────────────────────
	r.NoRoute(func(c *gin.Context) {
		utils.NotFound(c, "route not found")
	})

	return r
}

// ── Middleware implementations ─────────────────────────────────────────────────

// zapLogger logs every request using the platform's structured logger.
func zapLogger(log *utils.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start  := time.Now()
		path   := c.Request.URL.Path
		query  := c.Request.URL.RawQuery

		c.Next()

		fields := []any{
			"method",     c.Request.Method,
			"path",       path,
			"query",      query,
			"status",     c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"ip",         c.ClientIP(),
			"request_id", c.GetHeader("X-Request-ID"),
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Errorw("request", fields...)
		case c.Writer.Status() >= 400:
			log.Warnw("request", fields...)
		default:
			log.Infow("request", fields...)
		}
	}
}

// recovery catches panics and returns a 500 without crashing the server.
func recovery(log *utils.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Errorw("panic recovered",
					"err",    err,
					"path",   c.Request.URL.Path,
					"method", c.Request.Method,
				)
				utils.InternalError(c, "unexpected server error")
			}
		}()
		c.Next()
	}
}

// requestID injects a unique request ID into every response header.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = generateID()
		}
		c.Header("X-Request-ID", id)
		c.Set("request_id", id)
		c.Next()
	}
}

// corsHeaders allows the frontend origin to call the API.
func corsHeaders(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := "http://localhost:5173"
		if cfg.IsProd() {
			origin = "*" // tighten to real domain in production
		}
		c.Header("Access-Control-Allow-Origin",  origin)
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin,Content-Type,Authorization,X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// secureHeaders sets basic security response headers.
func secureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options",        "DENY")
		c.Header("Referrer-Policy",        "strict-origin-when-cross-origin")
		c.Next()
	}
}

// bodyLimit aborts requests whose body exceeds maxMB (outside of the
// multipart upload route which handles its own limit via MaxMultipartMemory).
func bodyLimit(maxMB int64) gin.HandlerFunc {
	limit := maxMB * 1024 * 1024
	return func(c *gin.Context) {
		// Skip for the upload route — handled by Gin's multipart reader
		if c.Request.URL.Path == "/api/v1/submissions" && c.Request.Method == http.MethodPost {
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// generateID returns a short unique ID for request correlation.
func generateID() string {
	return uuid.NewString()[:12]
}
