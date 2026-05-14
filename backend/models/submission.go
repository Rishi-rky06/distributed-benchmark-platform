package models

import "time"

// SubmissionStatus is the lifecycle state of a contestant upload.
type SubmissionStatus string

const (
	SubmissionPending   SubmissionStatus = "pending"
	SubmissionBuilding  SubmissionStatus = "building"
	SubmissionReady     SubmissionStatus = "ready"
	SubmissionRunning   SubmissionStatus = "running"
	SubmissionCompleted SubmissionStatus = "completed"
	SubmissionFailed    SubmissionStatus = "failed"
)

// Submission represents a contestant's uploaded trading engine.
type Submission struct {
	ID          string           `db:"id"           json:"id"`
	UserID      string           `db:"user_id"      json:"user_id"`
	TeamName    string           `db:"team_name"    json:"team_name"`
	Filename    string           `db:"filename"     json:"filename"`
	Language    string           `db:"language"     json:"language"` // go | cpp | rust
	Status      SubmissionStatus `db:"status"       json:"status"`
	ContainerID string           `db:"container_id" json:"container_id,omitempty"`
	ImageTag    string           `db:"image_tag"    json:"image_tag,omitempty"`
	ErrorMsg    *string          `db:"error_msg"    json:"error_msg,omitempty"`
	CreatedAt   time.Time        `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time        `db:"updated_at"   json:"updated_at"`
}

// CreateSubmissionRequest is the parsed body / form for a new submission.
type CreateSubmissionRequest struct {
	TeamName string `form:"team_name" binding:"required,min=2,max=64"`
	Language string `form:"language"  binding:"required,oneof=go cpp rust"`
}
