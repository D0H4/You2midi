//go:build !windows

package updater

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// WaitForPIDExit waits for a process to exit.
func WaitForPIDExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("check process %d: %w", pid, err)
		}

		if timeout > 0 && time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for process %d to exit", pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
