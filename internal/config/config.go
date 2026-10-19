// Package config resolves the data directory, sets up logging and owns the
// default application settings.
package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Environment variables understood by gomail.
const (
	EnvDataDir  = "GOMAIL_DATA_DIR"
	EnvDemo     = "GOMAIL_DEMO"
	EnvLogLevel = "GOMAIL_LOG_LEVEL"
)

// DataDir returns (and creates) the directory where gomail keeps its
// database, logs and fallback secret store. GOMAIL_DATA_DIR overrides the
// per-OS default (os.UserConfigDir()/gomail: ~/.config/gomail on Linux,
// ~/Library/Application Support/gomail on macOS, %AppData%\gomail on Windows).
func DataDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv(EnvDataDir))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return "", fmt.Errorf("cannot determine a data directory: %w", err)
			}
			base = filepath.Join(home, ".config")
		}
		dir = filepath.Join(base, "gomail")
	}
	if DemoMode() {
		dir = filepath.Join(dir, "demo")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create data directory %s: %w", dir, err)
	}
	return dir, nil
}

// CustomProfile reports whether a non-default data directory is in use
// (GOMAIL_DATA_DIR or demo mode).
func CustomProfile() bool {
	return strings.TrimSpace(os.Getenv(EnvDataDir)) != "" || DemoMode()
}

// DemoMode reports whether GOMAIL_DEMO is set to a truthy value.
func DemoMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDemo))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Log rotation: gomail.log is capped at maxLogSize; older logs are kept
// as gomail.log.1 .. gomail.log.<keepLogs>.
const (
	maxLogSize = 5 << 20
	keepLogs   = 3
)

// rotatingFile is an io.Writer that rotates the log by size.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openRotating(path string) (*rotatingFile, error) {
	r := &rotatingFile{path: path}
	if fi, err := os.Stat(path); err == nil && fi.Size() >= maxLogSize {
		rotateFiles(path)
	}
	return r, r.open()
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, _ := f.Stat()
	r.f, r.size = f, 0
	if fi != nil {
		r.size = fi.Size()
	}
	return nil
}

func rotateFiles(path string) {
	_ = os.Remove(fmt.Sprintf("%s.%d", path, keepLogs))
	for i := keepLogs - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	_ = os.Rename(path, path+".1")
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return len(p), nil
	}
	if r.size+int64(len(p)) > maxLogSize {
		r.f.Close()
		rotateFiles(r.path)
		if err := r.open(); err != nil {
			r.f = nil
			return len(p), nil
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// SetupLogging installs a slog default logger writing to gomail.log in dir
// (size-rotated, 3 old files kept) and to stderr. It returns the log path
// and a closer for the file.
func SetupLogging(dir string) (string, io.Closer, error) {
	path := filepath.Join(dir, "gomail.log")
	f, err := openRotating(path)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})))
		return "", nil, err
	}
	h := slog.NewTextHandler(io.MultiWriter(f, os.Stderr), &slog.HandlerOptions{Level: logLevel()})
	slog.SetDefault(slog.New(h))
	return path, f, nil
}

func logLevel() slog.Level {
	switch strings.ToLower(os.Getenv(EnvLogLevel)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
