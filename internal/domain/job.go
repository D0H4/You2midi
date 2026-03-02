// Package domain contains pure interfaces and value types.
// This package MUST have zero external dependencies.
package domain

import "time"

// JobState represents the lifecycle state of a transcription job.
type JobState string

const (
	JobStateQueued    JobState = "queued"
	JobStateRunning   JobState = "running"
	JobStateRetrying  JobState = "retrying"
	JobStateCompleted JobState = "completed"
	JobStateFailed    JobState = "failed"
	JobStateCancelled JobState = "cancelled"
)

// Job holds all data for a single transcription request.
type Job struct {
	ID            string     `json:"id"`
	State         JobState   `json:"state"`
	YoutubeURL    string     `json:"youtube_url,omitempty"`
	AudioPath     string     `json:"audio_path,omitempty"` // local upload path
	WorkspacePath string     `json:"workspace_path"`
	AudioHash     string     `json:"audio_hash"`            // SHA256 of audio content
	Engine        string     `json:"engine"`                // "transkun" | "neuralnote"
	Device        string     `json:"device"`                // "cpu" | "cuda"
	StartSec      int        `json:"start_sec,omitempty"`   // trim input audio from this offset before inference
	OutputPath    string     `json:"output_path,omitempty"` // set on completion
	ErrorCode     string     `json:"error_code,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	Attempt       int        `json:"attempt"`
	MaxAttempts   int        `json:"max_attempts"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`

	// Stage timings (ms) populated during execution.
	DownloadMs  int64 `json:"download_ms,omitempty"`
	InferenceMs int64 `json:"inference_ms,omitempty"`
	TotalMs     int64 `json:"total_ms,omitempty"`
}

// IsTerminal returns true if the job has reached a final state.
func (j *Job) IsTerminal() bool {
	switch j.State {
	case JobStateCompleted, JobStateFailed, JobStateCancelled:
		return true
	}
	return false
}

// JobEvent is emitted when a job transitions state.
type JobEvent struct {
	JobID    string   `json:"job_id"`
	NewState JobState `json:"new_state"`
	Message  string   `json:"message,omitempty"`
}

// TranscribeOptions carries per-call engine options.
type TranscribeOptions struct {
	Device      string // "cpu" | "cuda"
	StartSec    int    // trim input audio from this offset before inference
	SegmentSize int    // seconds; 0 = engine default
	SegmentHop  int    // seconds; 0 = engine default
	PedalExtend bool   // extend notes by sustain-pedal duration
}
