package domain

import (
	"context"
	"time"
)

// JobRepository defines persistence operations for jobs.
// All implementations must be safe for concurrent use.
type JobRepository interface {
	// Create inserts a new job record and returns the created job (with ID populated).
	Create(ctx context.Context, job *Job) (*Job, error)

	// Get retrieves a job by ID.
	Get(ctx context.Context, id string) (*Job, error)

	// Update persists state changes for an existing job.
	Update(ctx context.Context, job *Job) error

	// List returns all jobs, newest first.
	List(ctx context.Context) ([]*Job, error)

	// ListByState returns jobs in the given state.
	ListByState(ctx context.Context, state JobState) ([]*Job, error)

	// Delete removes a job record. It does NOT clean up workspace files.
	Delete(ctx context.Context, id string) error
}

// JobPageRepository is an optional extension for paginated job listing.
// Implementations may expose this to avoid full-table scans for polling clients.
type JobPageRepository interface {
	// ListPage returns jobs ordered newest-first, limited to `limit`.
	// If before is provided, only rows older than before are returned.
	ListPage(ctx context.Context, limit int, before *time.Time) ([]*Job, error)
}

// CacheRepository defines the content-addressed MIDI cache.
// Cache key: hex(SHA256(audio)) + ":" + engine + ":" + engineVersion + ":" + hex(SHA256(params))
type CacheRepository interface {
	// Lookup returns (midiPath, true) if the cache key is present.
	Lookup(ctx context.Context, key string) (midiPath string, hit bool, err error)

	// Store writes an entry into the cache and returns the stored MIDI path.
	Store(ctx context.Context, key string, midiPath string) error

	// Invalidate removes a cache entry.
	Invalidate(ctx context.Context, key string) error
}
