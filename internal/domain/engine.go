package domain

import (
	"context"
	"io"
)

// TranscriptionEngine is the interface all engine adapters must implement.
// Adapters handle their own temp-file management internally; callers see only streams.
type TranscriptionEngine interface {
	// Name returns the engine identifier, e.g. "transkun" or "neuralnote".
	Name() string

	// Transcribe reads piano audio from input and writes a MIDI file to the
	// returned reader. The context must be respected for cancellation.
	Transcribe(ctx context.Context, input io.Reader, opts TranscribeOptions) (io.ReadCloser, error)

	// Version returns the engine version string (used as part of the cache key).
	Version(ctx context.Context) (string, error)

	// HealthCheck verifies that the engine binary and model files are available.
	HealthCheck(ctx context.Context) error
}
