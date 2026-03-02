//go:build windows

package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DefaultRunner is the Windows implementation of ProcessRunner.
// It uses a Windows Job Object to guarantee the entire child process tree is
// terminated when the context is cancelled or the timeout elapses.
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

	cmd := exec.CommandContext(runCtx, name, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	// Merge extra env vars with the current process environment.
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Create a Windows Job Object so child processes are killed with the parent.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("runner: CreateJobObject: %w", err)
	}
	defer windows.CloseHandle(job)

	// Set KILL_ON_JOB_CLOSE so the entire process tree is terminated when the
	// Job Object handle is closed (either normally or on our side crashing).
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return nil, fmt.Errorf("runner: SetInformationJobObject: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: start %q: %w", name, err)
	}

	// Open a handle to the child process so we can assign it to the Job Object.
	// We need PROCESS_ALL_ACCESS to use AssignProcessToJobObject.
	childHandle, err := windows.OpenProcess(
		windows.PROCESS_ALL_ACCESS, false, uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("runner: OpenProcess: %w", err)
	}
	defer windows.CloseHandle(childHandle)

	if err := windows.AssignProcessToJobObject(job, childHandle); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("runner: AssignProcessToJobObject: %w", err)
	}

	// Wait for completion in a goroutine so we can react to context cancellation.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-runCtx.Done():
		// Close the Job Object handle → Windows kills the entire process tree.
		windows.CloseHandle(job)
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
