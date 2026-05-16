package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/models"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// SubmissionHandler handles all /api/v1/submissions routes.
type SubmissionHandler struct {
	cfg   *config.Config
	log   *utils.Logger
	db    *config.DB
	rdb   *config.RedisClient
	queue *services.QueueService
}

func NewSubmissionHandler(
	cfg *config.Config,
	log *utils.Logger,
	db *config.DB,
	rdb *config.RedisClient,
	queue *services.QueueService,
) *SubmissionHandler {
	return &SubmissionHandler{cfg: cfg, log: log, db: db, rdb: rdb, queue: queue}
}

// ── POST /api/v1/submissions ─────────────────────────────────────────────────

// Create godoc
// @Summary     Upload a trading engine submission
// @Description Accepts a multipart form with team_name, language, and the source file.
//              Validates the upload, persists metadata to Postgres, and stages the
//              file to the submissions directory for the sandbox worker to pick up.
// @Tags        submissions
// @Accept      multipart/form-data
// @Produce     json
// @Param       team_name formData string true  "Team name (2–64 chars)"
// @Param       language  formData string true  "Language: go | cpp | rust"
// @Param       file      formData file   true  "Source archive or single source file"
// @Success     201 {object} models.Submission
// @Failure     400 {object} utils.Envelope
// @Failure     422 {object} utils.Envelope
// @Failure     500 {object} utils.Envelope
// @Router      /submissions [post]
func (h *SubmissionHandler) Create(c *gin.Context) {
	// ── Parse + validate form fields ──────────────────────────────────────────
	var req models.CreateSubmissionRequest
	if err := c.ShouldBind(&req); err != nil {
		utils.BadRequest(c, "invalid form fields: "+err.Error())
		return
	}

	lang, err := utils.NormalizeLanguage(req.Language)
	if err != nil {
		utils.UnprocessableEntity(c, err.Error())
		return
	}

	// ── File validation ───────────────────────────────────────────────────────
	fh, err := c.FormFile("file")
	if err != nil {
		utils.BadRequest(c, "file field is required")
		return
	}

	if err := utils.ValidateSubmissionFile(fh, lang, h.cfg.MaxUploadMB); err != nil {
		utils.UnprocessableEntity(c, err.Error())
		return
	}

	// ── Build submission record ───────────────────────────────────────────────
	sub := &models.Submission{
		ID:       uuid.NewString(),
		UserID:   c.GetString("user_id"), // set by auth middleware (placeholder)
		TeamName: req.TeamName,
		Filename: filepath.Base(fh.Filename),
		Language: lang,
		Status:   models.SubmissionPending,
	}

	// ── Stage file to disk ────────────────────────────────────────────────────
	destDir := filepath.Join(h.cfg.SubmissionsDir, sub.ID)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		h.log.Errorw("failed to create submission dir", "err", err, "id", sub.ID)
		utils.InternalError(c, "could not stage submission")
		return
	}

	destPath := filepath.Join(destDir, sub.Filename)
	if err := h.saveUpload(fh, destPath); err != nil {
		h.log.Errorw("file save failed", "err", err, "path", destPath)
		utils.InternalError(c, "could not save submission file")
		return
	}

	// ── Persist to Postgres ───────────────────────────────────────────────────
	const q = `
		INSERT INTO submissions (id, user_id, team_name, filename, language, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`

	row := h.db.QueryRowContext(
		c.Request.Context(), q,
		sub.ID, sub.UserID, sub.TeamName, sub.Filename, sub.Language, sub.Status,
	)
	if err := row.Scan(&sub.CreatedAt, &sub.UpdatedAt); err != nil {
		h.log.Errorw("db insert failed", "err", err)
		utils.InternalError(c, "failed to persist submission")
		return
	}

	h.log.Infow("submission created",
		"id", sub.ID, "team", sub.TeamName, "lang", sub.Language)

	utils.Created(c, sub)
}

// ── GET /api/v1/submissions ──────────────────────────────────────────────────

// List godoc
// @Summary  List submissions (paginated)
// @Tags     submissions
// @Produce  json
// @Param    page     query int    false "Page number (default 1)"
// @Param    page_size query int   false "Page size (default 20, max 100)"
// @Param    status   query string false "Filter by status"
// @Success  200 {array} models.Submission
// @Router   /submissions [get]
func (h *SubmissionHandler) List(c *gin.Context) {
	page, _     := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _     := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	statusFilter := c.Query("status")

	p := utils.ParsePagination(page, size)

	// Count total
	var total int64
	countQ := `SELECT COUNT(*) FROM submissions`
	args   := []any{}

	if statusFilter != "" {
		countQ += ` WHERE status = $1`
		args = append(args, statusFilter)
	}

	if err := h.db.QueryRowContext(c.Request.Context(), countQ, args...).Scan(&total); err != nil {
		h.log.Errorw("count query failed", "err", err)
		utils.InternalError(c, "database error")
		return
	}

	// Fetch page
	listQ := `
		SELECT id, user_id, team_name, filename, language, status,
		       COALESCE(container_id,'') AS container_id,
		       COALESCE(image_tag,'')    AS image_tag,
		       error_msg, created_at, updated_at
		FROM submissions`

	if statusFilter != "" {
		listQ += ` WHERE status = $3`
		args = append([]any{p.Size, p.Offset}, args...)
	} else {
		args = []any{p.Size, p.Offset}
	}
	listQ += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := h.db.QueryxContext(c.Request.Context(), listQ, args...)
	if err != nil {
		h.log.Errorw("list query failed", "err", err)
		utils.InternalError(c, "database error")
		return
	}
	defer rows.Close()

	subs := make([]models.Submission, 0, p.Size)
	for rows.Next() {
		var s models.Submission
		if err := rows.StructScan(&s); err != nil {
			h.log.Errorw("row scan failed", "err", err)
			continue
		}
		subs = append(subs, s)
	}

	utils.OKList(c, subs, utils.Meta{
		Page:       p.Page,
		PageSize:   p.Size,
		TotalItems: total,
		TotalPages: utils.TotalPages(total, p.Size),
	})
}

// ── GET /api/v1/submissions/:id ──────────────────────────────────────────────

// Get godoc
// @Summary  Get a single submission by ID
// @Tags     submissions
// @Produce  json
// @Param    id path string true "Submission UUID"
// @Success  200 {object} models.Submission
// @Failure  404 {object} utils.Envelope
// @Router   /submissions/{id} [get]
func (h *SubmissionHandler) Get(c *gin.Context) {
	id := c.Param("id")

	const q = `
		SELECT id, user_id, team_name, filename, language, status,
		       COALESCE(container_id,'') AS container_id,
		       COALESCE(image_tag,'')    AS image_tag,
		       error_msg, created_at, updated_at
		FROM submissions WHERE id = $1`

	var sub models.Submission
	if err := h.db.GetContext(c.Request.Context(), &sub, q, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.NotFound(c, fmt.Sprintf("submission %q not found", id))
			return
		}
		h.log.Errorw("get submission failed", "err", err, "id", id)
		utils.InternalError(c, "database error")
		return
	}

	utils.OK(c, sub)
}

// ── DELETE /api/v1/submissions/:id ───────────────────────────────────────────

// Delete godoc
// @Summary  Delete a submission (only when not running)
// @Tags     submissions
// @Produce  json
// @Param    id path string true "Submission UUID"
// @Success  204
// @Failure  404 {object} utils.Envelope
// @Failure  409 {object} utils.Envelope
// @Router   /submissions/{id} [delete]
func (h *SubmissionHandler) Delete(c *gin.Context) {
	id := c.Param("id")

	// Prevent deletion while a benchmark is active
	var status models.SubmissionStatus
	if err := h.db.QueryRowContext(c.Request.Context(),
		`SELECT status FROM submissions WHERE id = $1`, id,
	).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.NotFound(c, "submission not found")
			return
		}
		utils.InternalError(c, "database error")
		return
	}

	if status == models.SubmissionRunning || status == models.SubmissionBuilding {
		utils.Conflict(c, fmt.Sprintf(
			"cannot delete submission in %q state; wait for it to complete or fail", status,
		))
		return
	}

	if _, err := h.db.ExecContext(c.Request.Context(),
		`DELETE FROM submissions WHERE id = $1`, id,
	); err != nil {
		h.log.Errorw("delete submission failed", "err", err, "id", id)
		utils.InternalError(c, "database error")
		return
	}

	// Best-effort: clean up staged files
	_ = os.RemoveAll(filepath.Join(h.cfg.SubmissionsDir, id))

	utils.NoContent(c)
}

// ── POST /api/v1/submissions/:id/run ────────────────────────────────────────

// TriggerRun godoc
// @Summary  Trigger a benchmark run for a submission
// @Tags     submissions
// @Produce  json
// @Param    id path string true "Submission UUID"
// @Success  201 {object} models.BenchmarkRun
// @Failure  404 {object} utils.Envelope
// @Failure  409 {object} utils.Envelope
// @Router   /submissions/{id}/run [post]
func (h *SubmissionHandler) TriggerRun(c *gin.Context) {
	subID := c.Param("id")
	ctx   := c.Request.Context()

	// Verify submission exists and is in a runnable state
	var sub models.Submission
	if err := h.db.GetContext(ctx, &sub,
		`SELECT id, status, language FROM submissions WHERE id = $1`, subID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			utils.NotFound(c, "submission not found")
			return
		}
		utils.InternalError(c, "database error")
		return
	}

	if sub.Status != models.SubmissionPending && sub.Status != models.SubmissionReady && sub.Status != models.SubmissionCompleted {
		utils.Conflict(c, fmt.Sprintf(
			"submission is in %q state; must be 'pending', 'ready', or 'completed' to run", sub.Status,
		))
		return
	}

	// Create benchmark run record
	run := &models.BenchmarkRun{
		ID:            uuid.NewString(),
		SubmissionID:  subID,
		Status:        models.BenchmarkQueued,
		BotWorkers:    h.cfg.BotFleetMinWorkers,
		BotProtocol:   h.cfg.BotProtocol,
		BotOrderRate:  h.cfg.BotOrderRatePerWorker,
	}

	const q = `
		INSERT INTO benchmark_runs
		    (id, submission_id, status, bot_workers, bot_protocol, bot_order_rate)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`

	if err := h.db.QueryRowContext(ctx, q,
		run.ID, run.SubmissionID, run.Status,
		run.BotWorkers, run.BotProtocol, run.BotOrderRate,
	).Scan(&run.CreatedAt); err != nil {
		h.log.Errorw("insert benchmark_run failed", "err", err)
		utils.InternalError(c, "failed to create benchmark run")
		return
	}

	// Enqueue to Redis for the benchmark worker to pick up
	if err := h.queue.EnqueueRun(ctx, run.ID); err != nil {
		h.log.Warnw("failed to enqueue run — worker will poll", "err", err, "run_id", run.ID)
	}

	h.log.Infow("benchmark run queued",
		"run_id", run.ID, "submission_id", subID)

	utils.Created(c, run)
}

// ── GET /api/v1/submissions/:id/runs ────────────────────────────────────────

// ListRuns godoc
// @Summary  List all benchmark runs for a submission
// @Tags     submissions
// @Produce  json
// @Param    id path string true "Submission UUID"
// @Success  200 {array} models.BenchmarkRunSummary
// @Router   /submissions/{id}/runs [get]
func (h *SubmissionHandler) ListRuns(c *gin.Context) {
	subID := c.Param("id")

	const q = `
		SELECT br.id, br.submission_id, s.team_name, br.status,
		       br.duration_ms, br.created_at
		FROM benchmark_runs br
		JOIN submissions s ON s.id = br.submission_id
		WHERE br.submission_id = $1
		ORDER BY br.created_at DESC`

	rows, err := h.db.QueryxContext(c.Request.Context(), q, subID)
	if err != nil {
		h.log.Errorw("list runs failed", "err", err, "submission_id", subID)
		utils.InternalError(c, "database error")
		return
	}
	defer rows.Close()

	runs := make([]models.BenchmarkRunSummary, 0)
	for rows.Next() {
		var r models.BenchmarkRunSummary
		if err := rows.StructScan(&r); err != nil {
			h.log.Errorw("run row scan failed", "err", err)
			continue
		}
		runs = append(runs, r)
	}

	utils.OK(c, runs)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// saveUpload streams a multipart upload to destPath.
func (h *SubmissionHandler) saveUpload(fh *multipart.FileHeader, destPath string) error {
	src, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy upload: %w", err)
	}
	return nil
}

// ── compile-time interface assertions ────────────────────────────────────────
var (
	_ = time.Now // suppress unused import if time is only used indirectly
	_ = uuid.New // ensure uuid is imported
)
