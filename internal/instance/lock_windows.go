//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffsetHigh puts the locked byte at offset 4 GiB, far beyond the PID
// written at the start of the file: Windows byte-range locks are mandatory,
// so locking byte 0 would make the PID unreadable through other handles
// (including ours, e.g. os.ReadFile).
const lockOffsetHigh = 1

// lockFile takes an exclusive, non-blocking byte-range lock (LockFileEx);
// it conflicts across handles and is released when the process exits.
func lockFile(f *os.File) error {
	ol := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return ErrLocked
	}
	if err != nil {
		return fmt.Errorf("locking the instance file: %w", err)
	}
	return nil
}

func unlockFile(f *os.File) error {
	ol := windows.Overlapped{OffsetHigh: lockOffsetHigh}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
