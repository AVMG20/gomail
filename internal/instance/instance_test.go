package instance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Release) // before TempDir removal: Windows can't delete open files
	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid file %q", data)
	}
	l.Release()
	l.Release() // idempotent
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	t.Cleanup(l2.Release)
	if _, err := Acquire(filepath.Join(dir, "missing", "dir")); err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("unwritable dir: %v", err)
	}
}

// TestLockAcrossProcesses runs a child process that tries to take the lock
// this process holds.
func TestLockAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("GOMAIL_LOCK_CHILD"); dir != "" {
		if _, err := Acquire(dir); errors.Is(err, ErrLocked) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Release)
	run := func() int {
		cmd := exec.Command(os.Args[0], "-test.run=TestLockAcrossProcesses")
		cmd.Env = append(os.Environ(), "GOMAIL_LOCK_CHILD="+dir)
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := run(); code != 3 {
		t.Fatalf("child got the lock while it was held (exit %d)", code)
	}
	l.Release()
	if code := run(); code != 0 {
		t.Fatalf("child could not lock after release (exit %d)", code)
	}
}
