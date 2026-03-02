package runner

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// FakeRunner is a test double for ProcessRunner.
// It returns configurable stdout/stderr/exit codes and supports
// a PauseAt(n) hook for deterministic cancel testing (Issue #10-A).
type FakeRunner struct {
	mu      sync.Mutex
	calls   []FakeCall
	pauseAt int // pause after this many Run() calls; 0 = never
	paused  chan struct{}
	resumed chan struct{}
	cleaned bool
}

// FakeCall is the record of a single Run() invocation.
type FakeCall struct {
	Name     string
	Args     []string
	Opts     RunOptions
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Err      error
}

// NewFakeRunner creates a FakeRunner that immediately completes every call successfully.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		paused:  make(chan struct{}),
		resumed: make(chan struct{}),
	}
}

// PauseAt causes the FakeRunner to block after the n-th Run() call until Resume() is called.
// Returns the receiver for chaining.
func (f *FakeRunner) PauseAt(n int) *FakeRunner {
	f.pauseAt = n
	return f
}

// Resume unblocks a runner that is waiting at a PauseAt point.
func (f *FakeRunner) Resume() {
	select {
	case f.resumed <- struct{}{}:
	default:
	}
}

// WaitForPause blocks until the runner has reached the PauseAt sync point.
func (f *FakeRunner) WaitForPause() {
	<-f.paused
}

// MarkCleaned signals that the caller has performed cleanup (for cancel tests).
func (f *FakeRunner) MarkCleaned() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleaned = true
}

// WasCleaned returns true if MarkCleaned was called (used in assertions).
func (f *FakeRunner) WasCleaned() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleaned
}

// AddCall appends a scripted response for the next Run() invocation.
func (f *FakeRunner) AddCall(stdout []byte, stderr []byte, exitCode int, err error) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, FakeCall{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Err:      err,
	})
	return f
}

// Run implements ProcessRunner for testing. It replays scripted responses in order.
func (f *FakeRunner) Run(ctx context.Context, name string, args []string, opts RunOptions) (*RunResult, error) {
	f.mu.Lock()
	callIdx := len(f.calls)
	var call FakeCall
	if len(f.calls) > 0 {
		call = f.calls[0]
		f.calls = f.calls[1:]
	}
	f.mu.Unlock()

	// If PauseAt(n) was set and we've reached the n-th call, signal and wait.
	if f.pauseAt > 0 && callIdx+1 >= f.pauseAt {
		select {
		case f.paused <- struct{}{}:
		default:
		}
		// Wait for Resume() or context cancellation.
		select {
		case <-f.resumed:
		case <-ctx.Done():
			return nil, fmt.Errorf("fakerunner: %q: %w", name, ctx.Err())
		}
	}

	// Drain stdin if provided (callers may depend on this).
	if opts.Stdin != nil {
		_, _ = io.Copy(io.Discard, opts.Stdin)
	}

	if call.Err != nil {
		return nil, call.Err
	}

	return &RunResult{
		Stdout:   call.Stdout,
		Stderr:   call.Stderr,
		ExitCode: call.ExitCode,
		Duration: time.Millisecond,
	}, nil
}
