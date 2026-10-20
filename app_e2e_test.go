package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/core"
	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
)

// e2e drives the bound MailService against fake Google/Microsoft OAuth,
// Gmail, Graph and OpenRouter servers: everything below the Wails bridge
// is the production code path (loopback OAuth with PKCE, token refresh,
// provider clients, sync engine, store, AI client).
type e2e struct {
	t      *testing.T
	m      *MailService
	oauth  *fakes.OAuth
	gmail  *fakes.Gmail
	graph  *fakes.Graph
	gcal   *fakes.GoogleCalendar
	ai     *fakes.OpenRouter
	mu     sync.Mutex
	events map[string][]any
}

func (e *e2e) event(name string) []any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]any(nil), e.events[name]...)
}

// msg finds the local message with the given subject.
func (e *e2e) msg(accountID, subject string) models.MessageSummary {
	e.t.Helper()
	ids, err := e.m.ResolveQueryIDs(models.ListQuery{AccountID: accountID}, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	sums, err := e.m.svc.Store().Summaries(ids)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, s := range sums {
		if s.Subject == subject {
			return s
		}
	}
	e.t.Fatalf("no local message %q in %s", subject, accountID)
	return models.MessageSummary{}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newE2E(t *testing.T) *e2e {
	e := &e2e{t: t, oauth: fakes.NewOAuth(), ai: fakes.NewOpenRouter(), events: map[string][]any{}}
	e.gmail = fakes.NewGmail("alice@gmail.example", e.oauth.Valid)
	e.graph = fakes.NewGraph("bob@contoso.example", "Bob Builder", e.oauth.Valid)
	e.gcal = fakes.NewGoogleCalendar("alice@gmail.example", e.oauth.Valid)
	t.Cleanup(func() { e.oauth.Close(); e.gmail.Close(); e.graph.Close(); e.gcal.Close(); e.ai.Close() })

	oldGoogle, oldMS := auth.GoogleEndpoint, auth.MicrosoftAuthority
	auth.GoogleEndpoint = oauth2.Endpoint{AuthURL: e.oauth.URL() + "/google/auth", TokenURL: e.oauth.URL() + "/token", AuthStyle: oauth2.AuthStyleInParams}
	auth.MicrosoftAuthority = e.oauth.URL()
	t.Cleanup(func() { auth.GoogleEndpoint, auth.MicrosoftAuthority = oldGoogle, oldMS })

	t.Setenv("GOMAIL_DATA_DIR", t.TempDir())
	t.Setenv("GOMAIL_DEMO", "")
	e.m = NewMailService(nil)
	e.m.hooks = &testHooks{
		options: func(o *core.Options) {
			o.GmailBase, o.GraphBase, o.OpenRouterBase, o.GoogleCalendarBase = e.gmail.URL(), e.graph.URL(), e.ai.URL(), e.gcal.URL()
			o.Secrets = secrets.NewMemory()
			o.Online = func(context.Context) bool { return true }
		},
		openURL: e.oauth.Consent,
		emit: func(name string, data any) {
			e.mu.Lock()
			e.events[name] = append(e.events[name], data)
			e.mu.Unlock()
		},
	}
	if err := e.m.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil || e.m.initErr != nil {
		t.Fatal(err, e.m.initErr)
	}
	t.Cleanup(func() { _ = e.m.ServiceShutdown() })
	return e
}

func TestEndToEndFakeProviders(t *testing.T) {
	e := newE2E(t)
	m := e.m
	now := time.Now().Truncate(time.Second)

	// ---- server-side mailboxes ----
	travel := e.gmail.AddLabel("Travel")
	pdf := []byte("%PDF-1.4 fake invoice")
	e.gmail.Deliver(fakes.GmailMessage{ThreadID: "t1", LabelIDs: []string{"INBOX", "UNREAD", travel}, From: "Ann <ann@example.com>",
		To: "alice@gmail.example", Subject: "Trip planning", HTML: "<p>Where shall we go?</p>", Date: now.Add(-3 * time.Hour)})
	e.gmail.Deliver(fakes.GmailMessage{ThreadID: "t1", LabelIDs: []string{"INBOX", "UNREAD"}, From: "Ben <ben@example.com>",
		To: "alice@gmail.example", Subject: "Re: Trip planning", HTML: "<p>Lisbon!</p>", Date: now.Add(-2 * time.Hour)})
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX", "UNREAD"}, From: "Acme Billing <billing@acme.example>",
		To: "alice@gmail.example", Subject: "Invoice 2026-10", HTML: "<p>Your invoice is attached.</p>", Date: now.Add(-1 * time.Hour),
		Attachments: []fakes.Attachment{{Name: "invoice.pdf", MimeType: "application/pdf", Data: pdf}}})
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: nil, From: "Shop <shop@example.com>", To: "alice@gmail.example",
		Subject: "Old receipt", HTML: "<p>Thanks</p>", Date: now.Add(-48 * time.Hour)})

	e.graph.AddCategory("Travel")
	xlsx := bytes.Repeat([]byte("Q3,"), 100)
	g1 := e.graph.Deliver(fakes.GraphMessage{ConversationID: "c1", Folder: "inbox", Subject: "Quarterly report", FromName: "Carol",
		From: "carol@contoso.example", To: []string{"bob@contoso.example"}, Received: now.Add(-5 * time.Hour), HTML: "<p>See attached</p>",
		Attachments: []fakes.Attachment{{Name: "report.xlsx", MimeType: "application/vnd.ms-excel", Data: xlsx}}})
	e.graph.Deliver(fakes.GraphMessage{ConversationID: "c1", Folder: "inbox", Subject: "RE: Quarterly report", FromName: "Dan",
		From: "dan@contoso.example", To: []string{"bob@contoso.example"}, Received: now.Add(-4 * time.Hour), IsRead: true, HTML: "<p>Thanks</p>"})
	g3 := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Lunch?", FromName: "Eve", From: "eve@contoso.example",
		To: []string{"bob@contoso.example"}, Received: now.Add(-30 * time.Minute), HTML: "<p>12:30?</p>"})
	e.graph.Deliver(fakes.GraphMessage{Folder: "archive", Subject: "Archived note", From: "frank@contoso.example", IsRead: true,
		Received: now.Add(-72 * time.Hour)})

	// ---- sign-in (loopback + PKCE against the fake token endpoints) ----
	if _, err := m.AddAccount(models.ProviderGmail); err == nil || !strings.Contains(err.Error(), "one-time setup") || strings.Contains(err.Error(), "GOMAIL_") {
		t.Fatalf("expected missing client error, got %v", err)
	}
	st, _ := m.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret, st.MicrosoftClientID = "google-client", "google-secret", "ms-client"
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	ga, err := m.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	oa, err := m.AddAccount(models.ProviderOutlook)
	if err != nil {
		t.Fatal(err)
	}
	if ga.Email != "alice@gmail.example" || oa.Email != "bob@contoso.example" || oa.DisplayName != "Bob Builder" {
		t.Fatalf("accounts %+v %+v", ga, oa)
	}
	if ex, _, pkce := e.oauth.Stats(); ex != 2 || pkce != 0 {
		t.Fatalf("exchanges %d pkce failures %d", ex, pkce)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}

	// ---- conversations ----
	page, err := m.ListMessages(models.ListQuery{FolderKind: models.FolderInbox})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Messages) != 4 {
		t.Fatalf("inbox conversations: total %d rows %d", page.Total, len(page.Messages))
	}
	threads := map[string]int{}
	for _, r := range page.Messages {
		threads[r.Subject] = r.ThreadCount
	}
	if threads["Re: Trip planning"] != 2 || threads["RE: Quarterly report"] != 2 || threads["Lunch?"] != 1 {
		t.Fatalf("thread rows %v", threads)
	}
	if batches, missing := e.graph.Stats(); missing != 0 || batches == 0 {
		t.Fatalf("graph immutable ids missing on %d requests (batches %d)", missing, batches)
	}
	trip := e.msg(ga.ID, "Trip planning")
	if !containsString(trip.Labels, "travel") {
		t.Fatalf("provider label not imported: %v", trip.Labels)
	}

	// ---- bodies and attachments ----
	inv := e.msg(ga.ID, "Invoice 2026-10")
	full, err := m.GetMessage(inv.ID)
	if err != nil || !strings.Contains(full.BodyHTML, "invoice is attached") || len(full.Attachments) != 1 {
		t.Fatalf("gmail body %+v %v", full, err)
	}
	ad, err := m.GetAttachmentData(inv.ID, full.Attachments[0].ID)
	if got, _ := base64.StdEncoding.DecodeString(ad.DataBase64); err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("gmail attachment %v %q", err, got)
	}
	rep := e.msg(oa.ID, "Quarterly report")
	full, err = m.GetMessage(rep.ID)
	if err != nil || len(full.Attachments) != 1 || full.Attachments[0].Filename != "report.xlsx" {
		t.Fatalf("graph body %+v %v", full, err)
	}
	ad, err = m.GetAttachmentData(rep.ID, full.Attachments[0].ID)
	if got, _ := base64.StdEncoding.DecodeString(ad.DataBase64); err != nil || !bytes.Equal(got, xlsx) {
		t.Fatalf("graph attachment %v", err)
	}

	// ---- actions are pushed to the servers ----
	tripRow := ""
	for _, r := range page.Messages {
		if r.Subject == "Re: Trip planning" {
			tripRow = r.ConversationID // "c:<id>": acts on the whole thread
		}
	}
	if err := m.MarkRead([]string{tripRow}, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "gmail thread marked read", func() bool {
		for _, s := range []string{"Trip planning", "Re: Trip planning"} {
			gm, _ := e.gmail.Message(e.msg(ga.ID, s).RemoteID)
			if containsString(gm.LabelIDs, "UNREAD") {
				return false
			}
		}
		return true
	})
	lunch := e.msg(oa.ID, "Lunch?")
	if err := m.Archive([]string{lunch.ID}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "graph message archived", func() bool { return e.graph.FolderOf(g3) == "archive" })
	if err := m.SetStarred([]string{rep.ID}, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "graph flag", func() bool { gm, _ := e.graph.Message(g1); return gm.Flagged })

	// A provider label is written back (Gmail label / Outlook category).
	if err := m.AddLabel([]string{rep.ID}, "travel"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "outlook category", func() bool { gm, _ := e.graph.Message(g1); return containsString(gm.Categories, "Travel") })
	if err := m.AddLabel([]string{inv.ID}, "travel"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "gmail label", func() bool { gm, _ := e.gmail.Message(inv.RemoteID); return containsString(gm.LabelIDs, travel) })

	// ---- AI labels via the fake Decisions API ----
	e.ai.Score = func(q string, state map[string]any) float64 {
		if q == "label_finance" && strings.Contains(strings.ToLower(state["subject"].(string)), "invoice") {
			return 0.93
		}
		return 0.02
	}
	if err := m.SetOpenRouterKey("sk-or-e2e"); err != nil {
		t.Fatal(err)
	}
	res, err := m.ClassifyMessages([]string{inv.ID})
	if err != nil || len(res) != 1 || res[0].Error != "" || !containsString(res[0].Added, "finance") {
		t.Fatalf("classify %+v %v", res, err)
	}
	if decisions, _, withKey := e.ai.Calls("sk-or-e2e"); decisions == 0 || withKey == 0 {
		t.Fatal("decisions API not called with the key")
	}
	counts, _ := m.GetLabelCounts("")
	if n := labelCount(counts, "finance"); n != 1 {
		t.Fatalf("finance count %d (%+v)", n, counts)
	}

	// ---- send with attachment ----
	att := models.OutgoingAttachment{Filename: "notes.txt", MimeType: "text/plain", DataBase64: base64.StdEncoding.EncodeToString([]byte("hello attachment"))}
	if err := m.SendMessage(models.ComposeRequest{AccountID: ga.ID, Mode: "new", To: []models.Address{{Email: "ann@example.com"}},
		Subject: "Itinerary", BodyHTML: "<p>Attached.</p>", Attachments: []models.OutgoingAttachment{att}}); err != nil {
		t.Fatal(err)
	}
	sent := e.gmail.Sent()
	if len(sent) != 1 || sent[0].Subject != "Itinerary" || !regexp.MustCompile(`attachment; filename="?notes\.txt`).Match(sent[0].Raw) ||
		!bytes.Contains(sent[0].Raw, []byte(base64.StdEncoding.EncodeToString([]byte("hello attachment")))) {
		t.Fatalf("gmail sent %d: %s", len(sent), sent[0].Raw)
	}
	if err := m.SendMessage(models.ComposeRequest{AccountID: oa.ID, Mode: "reply", ReplyToID: rep.ID, To: []models.Address{{Email: "carol@contoso.example"}},
		Subject: "RE: Quarterly report", BodyHTML: "<p>Looks good</p>", Attachments: []models.OutgoingAttachment{att}}); err != nil {
		t.Fatal(err)
	}
	gsent := e.graph.Sent()
	if len(gsent) != 1 || gsent[0].ConversationID != "c1" || len(gsent[0].Attachments) != 1 || string(gsent[0].Attachments[0].Data) != "hello attachment" {
		t.Fatalf("graph sent %+v", gsent)
	}

	// ---- drafts: save, coalesced update, delete ----
	for _, acct := range []models.Account{ga, oa} {
		req := models.ComposeRequest{AccountID: acct.ID, Mode: "new", To: []models.Address{{Email: "x@example.com"}}, Subject: "Draft", BodyHTML: "<p>v1</p>"}
		h, err := m.SaveDraft(req, "")
		if err != nil {
			t.Fatal(err)
		}
		req.BodyHTML = "<p>v2</p>"
		if h2, err := m.SaveDraft(req, h); err != nil || h2 != h {
			t.Fatalf("second save %q %v", h2, err)
		}
		drafts := func() int {
			if acct.Provider == models.ProviderGmail {
				return e.gmail.Drafts()
			}
			return e.graph.Drafts()
		}
		if n := drafts(); n != 1 {
			t.Fatalf("%s drafts %d", acct.Provider, n)
		}
		if err := m.DeleteDraft(h); err != nil {
			t.Fatal(err)
		}
		if n := drafts(); n != 0 {
			t.Fatalf("%s drafts after delete %d", acct.Provider, n)
		}
	}

	// ---- incremental sync: new mail on both servers ----
	e.gmail.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX", "UNREAD"}, From: "Zoe <zoe@example.com>", To: "alice@gmail.example", Subject: "Fresh gmail", HTML: "<p>new</p>"})
	e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Fresh outlook", From: "yan@contoso.example", To: []string{"bob@contoso.example"}, HTML: "<p>new</p>"})
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	page, _ = m.ListMessages(models.ListQuery{FolderKind: models.FolderInbox})
	subjects := map[string]bool{}
	for _, r := range page.Messages {
		subjects[r.Subject] = true
	}
	if !subjects["Fresh gmail"] || !subjects["Fresh outlook"] || subjects["Lunch?"] {
		t.Fatalf("inbox after incremental sync %v", subjects)
	}
	if len(e.event(models.EventNewMail)) == 0 {
		t.Fatal("no mail:new event for incremental mail")
	}
	if res, err := m.SearchMessages(models.SearchQuery{Query: "invoice"}); err != nil || len(res) == 0 || res[0].Message.Subject != "Invoice 2026-10" {
		t.Fatalf("search %+v %v", res, err)
	}

	// ---- revoked sign-in: 401 -> refresh -> invalid_grant -> reauth ----
	e.oauth.ExpireAccessTokens()
	e.oauth.RevokeRefreshTokens()
	if err := m.SyncNow(ga.ID); err == nil {
		t.Fatal("sync with a revoked sign-in succeeded")
	}
	accts, _ := m.GetAccounts()
	for _, a := range accts {
		if a.ID == ga.ID && !a.NeedsReauth {
			t.Fatalf("account not flagged for reauth: %+v", a)
		}
	}
	if _, err := m.ReauthAccount(ga.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReauthAccount(oa.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	accts, _ = m.GetAccounts()
	for _, a := range accts {
		if a.NeedsReauth || a.SyncError != "" {
			t.Fatalf("account after reauth: %+v", a)
		}
	}
	// Refresh still works for a valid refresh token.
	e.oauth.ExpireAccessTokens()
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if _, refreshes, _ := e.oauth.Stats(); refreshes == 0 {
		t.Fatal("tokens were not refreshed")
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func labelCount(counts []models.LabelCount, id string) int {
	for _, c := range counts {
		if c.LabelID == id {
			return c.Total
		}
	}
	return 0
}
