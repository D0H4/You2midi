package neuralnote

import (
	"bytes"
	"context"
	"testing"

	"you2midi/internal/domain"
	"you2midi/internal/runner"
)

type stubRunner struct {
	res *runner.RunResult
	err error
}

func (r *stubRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOptions) (*runner.RunResult, error) {
	return r.res, r.err
}

func TestTranscribe_ClassifiesEngineNotFound(t *testing.T) {
	t.Parallel()
	a := New("neuralnote", &stubRunner{res: &runner.RunResult{ExitCode: 1, Stderr: []byte("not found")}})

	_, err := a.Transcribe(context.Background(), bytes.NewReader([]byte("audio")), domain.TranscribeOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	engErr, ok := err.(*domain.EngineError)
	if !ok {
		t.Fatalf("expected EngineError, got %T", err)
	}
	if engErr.Code != domain.ErrEngineNotFound {
		t.Fatalf("expected ENGINE_NOT_FOUND, got %s", engErr.Code)
	}
}
