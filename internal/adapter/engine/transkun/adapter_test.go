package transkun

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

func TestTranscribe_ClassifiesOOM(t *testing.T) {
	t.Parallel()
	a := New("transkun", "cuda", &stubRunner{res: &runner.RunResult{ExitCode: 1, Stderr: []byte("CUDA out of memory")}})

	_, err := a.Transcribe(context.Background(), bytes.NewReader([]byte("audio")), domain.TranscribeOptions{Device: "cuda"})
	if err == nil {
		t.Fatal("expected error")
	}
	engErr, ok := err.(*domain.EngineError)
	if !ok {
		t.Fatalf("expected EngineError, got %T", err)
	}
	if engErr.Code != domain.ErrOOM {
		t.Fatalf("expected OOM, got %s", engErr.Code)
	}
}

func TestExtractProgressPercents(t *testing.T) {
	t.Parallel()
	stderr := []byte("step 1 3.5%\nnoise\nstep 2 40%\nstep 2 40%\nstep 3 99.9%\n")
	got := extractProgressPercents(stderr)
	if len(got) != 3 {
		t.Fatalf("expected 3 progress points, got %d (%v)", len(got), got)
	}
	if got[0] != 3.5 || got[1] != 40 || got[2] != 99.9 {
		t.Fatalf("unexpected progress values: %v", got)
	}
}
