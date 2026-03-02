//go:build !windows

package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// DefaultRunner is the Unix implementation of ProcessRunner.
// It sets Setpgid=true so the child gets its own process group, then sends
// SIGKILL to the entire group on cancellation — preventing zombie processes.
type DefaultRunner struct{}

// NewDefaultRunner returns a platform-specific ProcessRunner.
func NewDefaultRunner() ProcessRunner { return &DefaultRunner{} }

func (r *DefaultRunner) Run(ctx context.Context, name string, args []string, opts RunOptions) (*RunResult, error) {
	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.Command(name, args...)
	// Place the child in its own process group so we can kill the entire tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: start %q: %w", name, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-runCtx.Done():
		// Kill the entire process group (negative PID = group ID).
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
		return nil, fmt.Errorf("runner: %q: %w", name, runCtx.Err())
	case waitErr := <-done:
		duration := time.Since(start)
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, fmt.Errorf("runner: wait %q: %w", name, waitErr)
			}
		}
		return &RunResult{
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
			ExitCode: exitCode,
			Duration: duration,
		}, nil
	}
}
