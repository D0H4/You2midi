package common

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"you2midi/internal/domain"
)

func TestClassifyStderr(t *testing.T) {
	t.Parallel()

	oom := ClassifyStderr([]byte("CUDA out of memory"), true)
	if oom.Code != domain.ErrOOM {
		t.Fatalf("expected OOM, got %s", oom.Code)
	}

	notFound := ClassifyStderr([]byte("binary not found"), false)
	if notFound.Code != domain.ErrEngineNotFound {
		t.Fatalf("expected ENGINE_NOT_FOUND, got %s", notFound.Code)
	}

	unknown := ClassifyStderr([]byte("something else"), false)
	if unknown.Code != domain.ErrUnknown {
		t.Fatalf("expected UNKNOWN, got %s", unknown.Code)
	}
}

func TestWrapRunError(t *testing.T) {
	t.Parallel()

	cancelled := WrapRunError(errors.New("context canceled"))
	if cancelled.Code != domain.ErrCancelled {
		t.Fatalf("expected CANCELLED, got %s", cancelled.Code)
	}

	timeout := WrapRunError(errors.New("context deadline exceeded"))
	if timeout.Code != domain.ErrTimeout {
		t.Fatalf("expected TIMEOUT, got %s", timeout.Code)
	}

	transient := WrapRunError(errors.New("process crashed"))
	if transient.Code != domain.ErrTransientCrash {
		t.Fatalf("expected TRANSIENT_CRASH, got %s", transient.Code)
	}
}

func TestWriteStreamAndCleanupReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	payload := []byte("abc123")

	if err := WriteStream(path, bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteStream: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q vs %q", got, payload)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cr := &CleanupReader{ReadCloser: f, Dir: dir}
	if err := cr.Close(); err != nil {
		t.Fatalf("CleanupReader.Close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup dir removed, stat err=%v", err)
	}
}
