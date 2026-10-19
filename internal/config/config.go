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

// DemoMode reports whether GOMAIL_DEMO is set to a truthy value.
func DemoMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDemo))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

const maxLogSize = 5 << 20

// SetupLogging installs a slog default logger writing to gomail.log in dir
// and to stderr. The previous log is rotated to gomail.log.1 when it grows
// beyond 5 MB. It returns the log path and a closer for the file.
func SetupLogging(dir string) (string, io.Closer, error) {
	path := filepath.Join(dir, "gomail.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
