package utils

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Envelope is the standard JSON wrapper for every API response.
type Envelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   *Err   `json:"error,omitempty"`
	Meta    *Meta  `json:"meta,omitempty"`
}

// Err carries a machine-readable code and human message.
type Err struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Meta carries pagination info for list endpoints.
type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

// ── Success ───────────────────────────────────────────────────────────────────

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Success: true, Data: data})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Envelope{Success: true, Data: data})
}

func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func OKList(c *gin.Context, data any, meta Meta) {
	c.JSON(http.StatusOK, Envelope{Success: true, Data: data, Meta: &meta})
}

// ── Errors ────────────────────────────────────────────────────────────────────

func errResp(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, Envelope{
		Success: false,
		Error:   &Err{Code: code, Message: msg},
	})
}

func BadRequest(c *gin.Context, msg string)          { errResp(c, 400, "BAD_REQUEST", msg) }
func Unauthorized(c *gin.Context, msg string)         { errResp(c, 401, "UNAUTHORIZED", msg) }
func Forbidden(c *gin.Context, msg string)            { errResp(c, 403, "FORBIDDEN", msg) }
func NotFound(c *gin.Context, msg string)             { errResp(c, 404, "NOT_FOUND", msg) }
func Conflict(c *gin.Context, msg string)             { errResp(c, 409, "CONFLICT", msg) }
func UnprocessableEntity(c *gin.Context, msg string)  { errResp(c, 422, "UNPROCESSABLE_ENTITY", msg) }
func TooManyRequests(c *gin.Context)                  { errResp(c, 429, "RATE_LIMITED", "too many requests") }
func InternalError(c *gin.Context, msg string)        { errResp(c, 500, "INTERNAL_ERROR", msg) }
func ServiceUnavailable(c *gin.Context, msg string)   { errResp(c, 503, "SERVICE_UNAVAILABLE", msg) }
