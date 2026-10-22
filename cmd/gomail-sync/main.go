// Command gomail-sync is the device-sync server for gomail: it stores each
// account's end-to-end encrypted settings, labels and sign-ins so several
// computers stay in sync. Run it behind an HTTPS reverse proxy (see
// deploy/sync/README.md).
//
// Configuration (environment):
//
//	ADDR           listen address (default :8080)
//	DATA_DIR       database directory (default ./data)
//	SIGNUPS        open | closed: whether new sync accounts can be created
//	               (default open; existing ones keep working when closed)
//	MAX_DOC_MB     largest document (default 16)
//	QUOTA_MB       storage per account (default 64)
//	MAX_ACCOUNTS   most sync accounts the server takes (default 1000; 0 = no limit)
//	TRUST_PROXY    1 = count rate limits per X-Forwarded-For address, for
//	               requests from a private or loopback address (the proxy)
//	LOG_LEVEL      debug | info | warn | error (default info)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AVMG20/gomail/internal/syncserver"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		slog.Error("gomail-sync stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg := syncserver.Config{
		DataDir:    env("DATA_DIR", "./data"),
		TrustProxy: truthy(os.Getenv("TRUST_PROXY")),
	}
	switch strings.ToLower(env("SIGNUPS", "open")) {
	case "open":
		cfg.Signups = true
	case "closed":
	default:
		return errors.New("SIGNUPS must be open or closed")
	}
	var err error
	if cfg.MaxDocBytes, err = megabytes("MAX_DOC_MB", 16); err != nil {
		return err
	}
	quota, err := megabytes("QUOTA_MB", 64)
	if err != nil {
		return err
	}
	cfg.QuotaBytes = int64(quota)
	if cfg.MaxAccounts, err = strconv.Atoi(env("MAX_ACCOUNTS", "1000")); err != nil || cfg.MaxAccounts < 0 {
		return errors.New("MAX_ACCOUNTS must be a number (0 = no limit)")
	}
	srv, err := syncserver.New(cfg)
	if err != nil {
		return err
	}
	// Closed after the server stopped: it waits for requests still running.
	defer srv.Close()

	addr := env("ADDR", ":8080")
	hs := srv.HTTPServer(addr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	slog.Info("gomail-sync listening", "addr", addr, "dataDir", cfg.DataDir, "signups", cfg.Signups, "maxAccounts", cfg.MaxAccounts)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shutdown)
}

// healthcheck asks the running server's /healthz (for the container
// HEALTHCHECK: the image has no shell or curl).
func healthcheck() int {
	addr := env("ADDR", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, resp.Status)
		return 1
	}
	return 0
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func megabytes(key string, def int) (int, error) {
	v := env(key, strconv.Itoa(def))
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 4096 {
		return 0, fmt.Errorf("%s must be a number of megabytes (1–4096)", key)
	}
	return n << 20, nil
}
