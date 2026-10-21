package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/AVMG20/gomail/internal/core"
	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/syncserver"
)

// peer starts a second app instance (another computer) against the same
// fake providers, with its own data folder and keychain.
func (e *e2e) peer() *MailService {
	e.t.Helper()
	m := NewMailService(nil)
	m.dataDir = e.t.TempDir()
	m.hooks = &testHooks{
		options: func(o *core.Options) {
			o.GmailBase, o.GraphBase, o.OpenRouterBase, o.GoogleCalendarBase = e.gmail.URL(), e.graph.URL(), e.ai.URL(), e.gcal.URL()
			o.Secrets = secrets.NewMemory()
			o.Online = func(context.Context) bool { return true }
		},
		openURL: e.oauth.Consent,
	}
	if err := m.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil || m.initErr != nil {
		e.t.Fatal(err, m.initErr)
	}
	e.t.Cleanup(func() { _ = m.ServiceShutdown() })
	return m
}

func labelNames(t *testing.T, m *MailService, accountID string) []string {
	t.Helper()
	st, err := m.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range st.Labels {
		if slices.Contains(l.AccountIDs, accountID) {
			out = append(out, l.Name+" "+l.Color)
		}
	}
	slices.Sort(out)
	return out
}

func devicesSync(t *testing.T, ms ...*MailService) {
	t.Helper()
	for _, m := range ms {
		if st, err := m.SyncDevicesNow(); err != nil || st.Error != "" {
			t.Fatalf("device sync: %v %+v", err, st)
		}
	}
}

// Two computers converge on settings, labels and label decisions through a
// sync server; the second one also gets the Gmail sign-in and reads mail
// without signing in itself.
func TestDeviceSyncTwoAppInstances(t *testing.T) {
	srv, err := syncserver.New(syncserver.Config{DataDir: t.TempDir(), Signups: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); srv.Close() })

	e := newE2E(t)
	work := e.m
	now := time.Now().Truncate(time.Second)
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX", "UNREAD"}, From: "Acme Billing <billing@acme.example>", To: "alice@gmail.example",
		Subject: "Invoice 2026-10", HTML: "<p>Your invoice.</p>", Date: now.Add(-time.Hour)})
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX"}, From: "Ann <ann@example.com>", To: "alice@gmail.example",
		Subject: "Lunch", HTML: "<p>Noon?</p>", Date: now.Add(-2 * time.Hour)})

	st, _ := work.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret = "google-client", "google-secret"
	if err := work.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	ga, err := work.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	if err := work.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	st, _ = work.GetSettings()
	st.Theme, st.Density = "dark", "compact"
	st.Labels = append(st.Labels, models.Label{Name: "Receipts", Color: "#0f766e", Description: "Invoices and receipts.", AccountIDs: []string{ga.ID}})
	if err := work.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	receipts := ""
	st, _ = work.GetSettings()
	for _, l := range st.Labels {
		if l.Name == "Receipts" {
			receipts = l.ID
		}
	}
	invoice := e.msg(ga.ID, "Invoice 2026-10")
	if _, err := work.AddLabel([]string{invoice.ID}, receipts); err != nil {
		t.Fatal(err)
	}

	if status, err := work.GetDeviceSyncStatus(); err != nil || status.Connected || status.DefaultServerURL != "https://gomail.avmg.nl" || !status.Available {
		t.Fatalf("status before connecting %+v %v", status, err)
	}
	if _, err := work.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: ts.URL, Create: true, IncludeSecrets: true}); err != nil {
		t.Fatal(err)
	}
	code, err := work.GetDeviceSyncCode()
	if err != nil {
		t.Fatal(err)
	}
	devicesSync(t, work)

	// ---- the home computer joins ----
	home := e.peer()
	// Typed in on the other computer: numbered, over several lines.
	var typed []string
	for i, w := range strings.Fields(code) {
		typed = append(typed, fmt.Sprintf("%d. %s", i+1, w))
	}
	if _, err := home.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: ts.URL, Code: strings.Join(typed, "\n")}); err != nil {
		t.Fatal(err)
	}
	devicesSync(t, home)
	accts, err := home.GetAccounts()
	if err != nil || len(accts) != 1 || accts[0].Email != "alice@gmail.example" || accts[0].NeedsReauth {
		t.Fatalf("home mailboxes %+v %v", accts, err)
	}
	hg := accts[0]
	// It reads the mailbox with the synced sign-in (refreshing it at the
	// fake Google token endpoint), without a sign-in of its own.
	exchanges, _, _ := e.oauth.Stats()
	if err := home.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if ex, refreshes, _ := e.oauth.Stats(); ex != exchanges || refreshes == 0 {
		t.Fatalf("home should refresh the synced sign-in: exchanges %d->%d, refreshes %d", exchanges, ex, refreshes)
	}
	hs, _ := home.GetSettings()
	if hs.Theme != "dark" || hs.Density != "compact" || hs.GoogleClientID != "google-client" {
		t.Fatalf("home settings: %s %s %q", hs.Theme, hs.Density, hs.GoogleClientID)
	}
	if w, h := labelNames(t, work, ga.ID), labelNames(t, home, hg.ID); !slices.Equal(w, h) {
		t.Fatalf("labels differ:\nwork %v\nhome %v", w, h)
	}
	hReceipts := ""
	for _, l := range hs.Labels {
		if l.Name == "Receipts" {
			hReceipts = l.ID
		}
	}
	eh := &e2e{t: t, m: home}
	if m := eh.msg(hg.ID, "Invoice 2026-10"); !slices.Contains(m.Labels, hReceipts) {
		t.Fatalf("label decision didn't sync: %v (Receipts is %s)", m.Labels, hReceipts)
	}

	// ---- changes at home show up at work ----
	hs.Theme = "light"
	for i, l := range hs.Labels {
		if l.ID == hReceipts {
			hs.Labels[i].Color = "#b91c1c"
		}
	}
	if err := home.SaveSettings(hs); err != nil {
		t.Fatal(err)
	}
	lunch := eh.msg(hg.ID, "Lunch")
	if _, err := home.AddLabel([]string{lunch.ID}, hReceipts); err != nil {
		t.Fatal(err)
	}
	devicesSync(t, home, work)
	ws, _ := work.GetSettings()
	if ws.Theme != "light" {
		t.Fatalf("work theme %s", ws.Theme)
	}
	if w, h := labelNames(t, work, ga.ID), labelNames(t, home, hg.ID); !slices.Equal(w, h) || !slices.Contains(w, "Receipts #b91c1c") {
		t.Fatalf("labels differ:\nwork %v\nhome %v", w, h)
	}
	if m := e.msg(ga.ID, "Lunch"); !slices.Contains(m.Labels, receipts) {
		t.Fatalf("home's label on Lunch didn't reach work: %v", m.Labels)
	}
	if status, _ := work.GetDeviceSyncStatus(); !status.Connected || status.LastSyncAt == nil || !status.IncludeSecrets {
		t.Fatalf("work status %+v", status)
	}
}
