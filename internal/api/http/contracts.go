// Package http contains request/response contracts derived from api/openapi.yaml.
package http

import "time"

// JobState mirrors OpenAPI JobState.
type JobState string

const (
	JobStateQueued    JobState = "queued"
	JobStateRunning   JobState = "running"
	JobStateRetrying  JobState = "retrying"
	JobStateCompleted JobState = "completed"
	JobStateFailed    JobState = "failed"
	JobStateCancelled JobState = "cancelled"
)

// JobStage mirrors OpenAPI JobProgress.stage.
type JobStage string

const (
	JobStageQueued       JobStage = "queued"
	JobStageRetrying     JobStage = "retrying"
	JobStageDownloading  JobStage = "downloading"
	JobStageTranscribing JobStage = "transcribing"
	JobStageFinalizing   JobStage = "finalizing"
	JobStageCompleted    JobStage = "completed"
	JobStageFailed       JobStage = "failed"
	JobStageCancelled    JobStage = "cancelled"
)

// CreateJobRequest mirrors components.schemas.CreateJobRequest.
type CreateJobRequest struct {
	YoutubeURL string `json:"youtube_url"`
	Device     string `json:"device,omitempty"`
	StartSec   int    `json:"start_sec,omitempty"`
}

// ErrorResponse mirrors components.schemas.ErrorResponse.
type ErrorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// HealthResponse mirrors components.schemas.HealthResponse.
type HealthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// JobProgress mirrors components.schemas.JobProgress.
type JobProgress struct {
	JobID        string    `json:"job_id"`
	State        JobState  `json:"state"`
	Stage        JobStage  `json:"stage"`
	ProgressPct  int       `json:"progress_pct"`
	Attempt      int       `json:"attempt"`
	MaxAttempts  int       `json:"max_attempts"`
	Cancelable   bool      `json:"cancelable"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	DownloadMs   int64     `json:"download_ms,omitempty"`
	InferenceMs  int64     `json:"inference_ms,omitempty"`
	TotalMs      int64     `json:"total_ms,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}
