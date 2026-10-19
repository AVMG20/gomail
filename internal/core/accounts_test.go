package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/update"
)

// consentUI signs in through the fake OAuth server.
type consentUI struct {
	fakeUI
	oauth *fakes.OAuth
}

func (c *consentUI) OpenURL(u string) error { return c.oauth.Consent(u) }

type realEnv struct {
	s     *Service
	ui    *consentUI
	oauth *fakes.OAuth
	gmail *fakes.Gmail
	graph *fakes.Graph
	ai    *fakes.OpenRouter
}

func newRealEnv(t *testing.T) *realEnv {
	t.Helper()
	e := &realEnv{oauth: fakes.NewOAuth(), ai: fakes.NewOpenRouter()}
	e.gmail = fakes.NewGmail("carol@gmail.example", e.oauth.Valid)
	e.graph = fakes.NewGraph("dave@contoso.example", "Dave", e.oauth.Valid)
	t.Cleanup(func() { e.oauth.Close(); e.gmail.Close(); e.graph.Close(); e.ai.Close() })
	oldG, oldM := auth.GoogleEndpoint, auth.MicrosoftAuthority
	auth.GoogleEndpoint = oauth2.Endpoint{TokenURL: e.oauth.URL() + "/token", AuthURL: e.oauth.URL() + "/auth", AuthStyle: oauth2.AuthStyleInParams}
	auth.MicrosoftAuthority = e.oauth.URL()
	t.Cleanup(func() { auth.GoogleEndpoint, auth.MicrosoftAuthority = oldG, oldM })
	e.ui = &consentUI{oauth: e.oauth}
	s, err := New(Options{DataDir: t.TempDir(), Version: "1.0.0", UI: e.ui, Secrets: secrets.NewMemory(),
		GmailBase: e.gmail.URL(), GraphBase: e.graph.URL(), OpenRouterBase: e.ai.URL(),
		Online: func(context.Context) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	e.s = s
	st := s.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret, st.MicrosoftClientID = "g", "gs", "m"
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAccountsLifecycleWithFakeProviders(t *testing.T) {
	e := newRealEnv(t)
	s := e.s
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX", "UNREAD"}, From: "Ann <ann@example.com>", Subject: "Hi Carol", HTML: "<p>hi</p>",
		Date: time.Now().Add(-time.Hour)})
	for i := 0; i < 3; i++ {
		e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX"}, From: "old@example.com", Subject: "Ancient " + string(rune('A'+i)),
			Date: time.Now().AddDate(-2, 0, -i)})
	}
	e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Hi Dave", From: "eve@contoso.example"})

	if _, err := s.AddAccount("imap"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	g, err := s.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.AddAccount(models.ProviderOutlook)
	if err != nil {
		t.Fatal(err)
	}
	if g.Color == o.Color || o.DisplayName != "Dave" || g.DisplayName != g.Email {
		t.Fatalf("accounts %+v %+v", g, o)
	}
	// Signing in again with the same mailbox reuses the account.
	again, err := s.AddAccount(models.ProviderGmail)
	if err != nil || again.ID != g.ID {
		t.Fatalf("re-add %+v %v", again, err)
	}
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	folders, err := s.Folders(g.ID)
	if err != nil || len(folders) < 5 {
		t.Fatalf("folders %d %v", len(folders), err)
	}
	ids, err := s.ResolveQueryIDs(models.ListQuery{AccountID: g.ID}, 100)
	if err != nil || len(ids) != 4 {
		t.Fatalf("gmail ids %d %v", len(ids), err)
	}

	// Remove/add a label locally (user label: not pushed).
	if err := s.AddLabel(ids[:1], "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabel(ids[:1], "work"); err != nil {
		t.Fatal(err)
	}

	// AI wrappers against the fake OpenRouter.
	if err := s.SetOpenRouterKey("sk-or-core"); err != nil {
		t.Fatal(err)
	}
	if err := s.TestOpenRouter(); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ListAIModels(true)
	if err != nil || len(ms) < 2 {
		t.Fatalf("models %d %v", len(ms), err)
	}
	if sum, err := s.SummarizeMessage(ids[0]); err != nil || sum != "Fake summary." {
		t.Fatalf("summary %q %v", sum, err)
	}
	if fixed, err := s.ImproveGrammar("helo"); err != nil || fixed == "" {
		t.Fatalf("grammar %q %v", fixed, err)
	}
	if _, err := s.ImproveGrammar(strings.Repeat("x", 100_001)); err == nil {
		t.Fatal("huge text accepted")
	}
	if _, chats, withKey := e.ai.Calls("sk-or-core"); chats != 2 || withKey < 3 {
		t.Fatalf("chats %d key uses %d", chats, withKey)
	}

	// Account settings.
	if err := s.UpdateAccount(g.ID, "  Carol  ", "#123456"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccount(g.ID, "", "red"); err == nil {
		t.Fatal("bad color accepted")
	}
	if err := s.UpdateAccount("missing", "x", ""); err == nil {
		t.Fatal("missing account")
	}
	if a, _ := s.store.Account(g.ID); a.DisplayName != "Carol" || a.Color != "#123456" {
		t.Fatalf("updated %+v", a)
	}

	// Re-authentication must use the same mailbox.
	e.gmail.SetEmail("mallory@gmail.example")
	if _, err := s.ReauthAccount(g.ID); err == nil || !strings.Contains(err.Error(), "mallory@gmail.example") {
		t.Fatalf("wrong mailbox accepted: %v", err)
	}
	e.gmail.SetEmail("carol@gmail.example")
	if _, err := s.ReauthAccount(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReauthAccount("missing"); err == nil {
		t.Fatal("reauth of a missing account")
	}
	s.CancelAddAccount()

	// Removing deletes tokens and data.
	if err := s.RemoveAccount(o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.LoadToken(s.secrets, o.ID); err == nil {
		t.Fatal("token kept after removal")
	}
	if err := s.RemoveAccount(o.ID); err == nil {
		t.Fatal("removed twice")
	}
	accts, _ := s.Accounts()
	if len(accts) != 1 {
		t.Fatalf("%d accounts left", len(accts))
	}
	s.resetProviders()
	if info := s.Info(); info.Version != "1.0.0" || info.DataDir == "" {
		t.Fatalf("info %+v", info)
	}
}

func TestUpdateCheckAndLoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.2.0","html_url":"https://example.com/r/v1.2.0","body":"Faster.","draft":false,"prerelease":false}`))
	}))
	defer srv.Close()
	oldURL, oldDelay := update.LatestURL, updateFirstDelay
	update.LatestURL, updateFirstDelay = srv.URL, 10*time.Millisecond
	oldStart := startDelay
	startDelay = 10 * time.Millisecond
	defer func() { update.LatestURL, updateFirstDelay, startDelay = oldURL, oldDelay, oldStart }()

	ui := &fakeUI{}
	s, err := New(Options{DataDir: t.TempDir(), Version: "1.1.0", UI: ui, Secrets: secrets.NewMemory(),
		Online: func(context.Context) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := s.CheckForUpdate()
	if err != nil || !info.Available || info.Latest != "1.2.0" || info.URL == "" || info.Current != "1.1.0" {
		t.Fatalf("update %+v %v", info, err)
	}
	// The loop skips the check made less than a day ago...
	var st updateState
	_ = s.store.GetSetting(updateStateKey, &st)
	st.LastCheck = time.Time{} // ...so pretend it is old.
	_ = s.store.SetSetting(updateStateKey, st)
	s.Start()
	deadline := time.Now().Add(5 * time.Second)
	for ui.count(models.EventUpdateAvailable) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no update:available event")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ev, _ := ui.last(models.EventUpdateAvailable).(models.UpdateInfo); ev.Latest != "1.2.0" {
		t.Fatalf("event %+v", ev)
	}
}

func TestSanitizeFilenamePortable(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf": "report.pdf", "../../etc/passwd": "passwd", `C:\Users\x\evil.exe`: "evil.exe", "a<b>:c?.txt": "a_b__c_.txt",
		"": "attachment", "..": "attachment", "trailing. . ": "trailing", "CON": "_CON", "nul.txt": "_nul.txt", "com1.log": "_com1.log",
		"COM10.txt": "COM10.txt", "console.txt": "console.txt", "tab\there": "tab_here",
	} {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
