package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestSingleInstanceFallback covers starting without the platform
// single-instance mechanism: gomail runs as a normal instance, and the
// data-directory lock keeps a second process out of the database.
func TestSingleInstanceFallback(t *testing.T) {
	if runtime.GOOS == "linux" {
		// No reachable session bus: the probe must say so instead of letting
		// Wails exit with a fatal error.
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "no-bus"))
		if singleInstanceUsable("com.avmg20.gomail.test") {
			t.Fatal("single instance reported usable without a session bus")
		}
	}
	if singleInstanceOptions(false, "com.avmg20.gomail", nil) != nil {
		t.Fatal("options without a usable mechanism")
	}
	if o := singleInstanceOptions(true, "com.avmg20.gomail", func(application.SecondInstanceData) {}); o == nil || o.UniqueID != "com.avmg20.gomail" {
		t.Fatalf("options %+v", o)
	}

	dir := t.TempDir()
	first, running := claimInstance(dir)
	if running || first == nil {
		t.Fatal("first instance could not claim the data directory")
	}
	if l, running := claimInstance(dir); !running || l != nil {
		t.Fatal("second instance was not detected")
	}
	// The second process never opens the database.
	t.Setenv("GOMAIL_DATA_DIR", dir)
	m := NewMailService(nil)
	m.initErr = errAlreadyRunning
	if err := m.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil || m.svc != nil {
		t.Fatalf("startup while locked: %v %v", err, m.svc)
	}
	if _, err := m.GetAccounts(); err == nil {
		t.Fatal("calls must fail with the init error")
	}
	if _, err := os.Stat(filepath.Join(dir, "gomail.db")); err == nil {
		t.Fatal("database opened by the second instance")
	}
	first.Release()
	if l, running := claimInstance(dir); running || l == nil {
		t.Fatal("lock not released")
	} else {
		l.Release()
	}
	// An unusable lock location only disables the guard.
	if l, running := claimInstance(filepath.Join(dir, "missing")); running || l != nil {
		t.Fatal("unusable lock must not block startup")
	}
}

// TestPresetStartup: main prepares the data dir and log once and the
// service reuses them.
func TestPresetStartup(t *testing.T) {
	t.Setenv("GOMAIL_DATA_DIR", t.TempDir())
	t.Setenv("GOMAIL_DEMO", "1")
	st, err := prepareStartup()
	if err != nil || st.dir == "" || st.logFile == "" {
		t.Fatalf("%+v %v", st, err)
	}
	m := NewMailService(nil)
	m.preset(st)
	if err := m.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil || m.initErr != nil {
		t.Fatal(err, m.initErr)
	}
	defer m.ServiceShutdown()
	if info := m.GetAppInfo(); info.DataDir != st.dir || info.LogFile != st.logFile {
		t.Fatalf("info %+v", info)
	}
}

func TestUIErrorIsReadable(t *testing.T) {
	for in, want := range map[error]string{
		errors.New("saving synced mail: database or disk is full (13)"): "disk is full",
		errors.New("database is locked (5) (SQLITE_BUSY)"):              "busy",
		errors.New("database disk image is malformed (11)"):             "damaged",
		errors.New("sql: Scan error on column index 2"):                 "local mail cache",
		context.Canceled: "cancelled",
		errors.New("Couldn't move messages on a@b.c: denied"): "Couldn't move messages on a@b.c: denied",
	} {
		if got := uiError(in); got == nil || !strings.Contains(got.Error(), want) || strings.Contains(got.Error(), "(13)") {
			t.Errorf("uiError(%v) = %v, want %q", in, got, want)
		}
	}
	if uiError(nil) != nil {
		t.Fatal("nil")
	}
	if v, err := uiResult(3, nil); v != 3 || err != nil {
		t.Fatal("uiResult")
	}
}
