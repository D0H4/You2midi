//go:build windows

package updater

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// WaitForPIDExit waits for a process to exit.
func WaitForPIDExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// Process may already be gone.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	waitMillis := uint32(windows.INFINITE)
	if timeout > 0 {
		waitMillis = uint32(timeout / time.Millisecond)
	}

	status, err := windows.WaitForSingleObject(handle, waitMillis)
	if err != nil {
		return fmt.Errorf("wait process %d: %w", pid, err)
	}
	if status == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("timed out waiting for process %d to exit", pid)
	}
	return nil
}
