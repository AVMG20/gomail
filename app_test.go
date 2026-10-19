package main

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/AVMG20/gomail/internal/models"
)

// TestServiceDemoSmoke boots the bound service headlessly in demo mode.
func TestServiceDemoSmoke(t *testing.T) {
	t.Setenv("GOMAIL_DATA_DIR", t.TempDir())
	t.Setenv("GOMAIL_DEMO", "1")
	m := NewMailService(nil)
	if err := m.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer m.ServiceShutdown()
	if m.initErr != nil {
		t.Fatal(m.initErr)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	accts, err := m.GetAccounts()
	if err != nil || len(accts) != 2 {
		t.Fatal(accts, err)
	}
	page, err := m.ListMessages(models.ListQuery{FolderKind: models.FolderInbox})
	if err != nil || page.Total == 0 {
		t.Fatal(page.Total, err)
	}
	if info := m.GetAppInfo(); !info.Demo || info.DataDir == "" || info.Version == "" {
		t.Fatalf("%+v", info)
	}
	if p := m.GetDefaultPrompts(); p.Grammar == "" || p.Classify == "" {
		t.Fatal("prompts")
	}
}
