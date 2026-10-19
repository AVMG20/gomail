// Package instance guards a data directory with an exclusive lock file so
// two gomail processes never share one database, even when the desktop's
// single-instance mechanism (D-Bus on Linux) is unavailable.
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("gomail is already running with this data directory")

// FileName is the lock file inside the data directory.
const FileName = "instance.lock"

// Lock is a held instance lock; the OS releases it if the process dies.
type Lock struct{ f *os.File }

// Acquire takes the lock for dir without blocking. It returns ErrLocked
// when another process (or another Acquire in this process) holds it.
func Acquire(dir string) (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the instance lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	// The pid is informational (support/debugging); errors don't matter.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{f: f}, nil
}

// Release gives the lock up. The file is kept: removing it could let two
// processes lock different files with the same name.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}
