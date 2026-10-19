package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
)

func TestConversationRowsAndActions(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	page, err := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var row models.MessageSummary
	subjects := map[string]int{}
	for _, m := range page.Messages {
		subjects[strings.TrimPrefix(m.Subject, "Re: ")]++
		if strings.Contains(m.Subject, "Q4 roadmap review") {
			row = m
		}
	}
	if subjects["Q4 roadmap review — slides attached"] != 1 {
		t.Fatalf("thread shown %d times", subjects["Q4 roadmap review — slides attached"])
	}
	if row.ThreadCount < 3 || row.ConversationID == "" || len(row.Participants) < 2 || !containsStr(row.Participants, "me") {
		t.Fatalf("row %+v", row)
	}
	// Message view setting.
	st := s.GetSettings()
	st.ConversationView = false
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	flat, _ := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox, Limit: 200})
	if flat.Total <= page.Total {
		t.Fatalf("message view total %d <= conversations %d", flat.Total, page.Total)
	}
	st.ConversationView = true
	s.SaveSettings(st)

	// Archive the conversation: inbox messages move, the sent reply stays.
	thread, _ := s.Thread(row.ID)
	sent, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderSent)
	inbox, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderInbox)
	if err := s.Archive([]string{row.ConversationID}); err != nil {
		t.Fatal(err)
	}
	for _, m := range thread {
		after, _, _ := s.store.Message(m.ID)
		if containsStr(after.FolderIDs, inbox.ID) {
			t.Fatalf("%q still in inbox", m.Subject)
		}
		if containsStr(m.FolderIDs, sent.ID) && !containsStr(after.FolderIDs, sent.ID) {
			t.Fatal("sent reply moved")
		}
	}
	// Mark the whole conversation unread, then read.
	if err := s.MarkRead([]string{row.ConversationID}, false); err != nil {
		t.Fatal(err)
	}
	sums, _ := s.store.Summaries(idsOf(thread))
	for _, m := range sums {
		if !m.Unread {
			t.Fatal("conversation not fully unread")
		}
	}
	if err := s.AddLabel([]string{row.ConversationID}, "important"); err != nil {
		t.Fatal(err)
	}
	sums, _ = s.store.Summaries(idsOf(thread))
	for _, m := range sums {
		if !containsStr(m.Labels, "important") {
			t.Fatal("label not applied to every message")
		}
	}
}

func idsOf(ms []models.Message) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestDraftsCoalescingAndSend(t *testing.T) {
	old := draftCoalesce
	draftCoalesce = 300 * time.Millisecond
	defer func() { draftCoalesce = old }()
	e := newEnv(t, "")
	s := e.s
	req := models.ComposeRequest{AccountID: demo.OutlookAccountID, Subject: "Draft v1", BodyHTML: "<p>one</p>"}
	id, err := s.SaveDraft(req, "")
	if err != nil || !strings.HasPrefix(id, "draft:"+demo.OutlookAccountID+":") {
		t.Fatalf("%q %v", id, err)
	}
	req.Subject = "Draft v2"
	id2, err := s.SaveDraft(req, id) // within the window: coalesced
	if err != nil || id2 != id {
		t.Fatalf("%q %v", id2, err)
	}
	req.Subject = "Draft v3"
	s.SaveDraft(req, id)
	time.Sleep(600 * time.Millisecond) // flush
	s.SyncNow(demo.OutlookAccountID)
	drafts, _ := s.store.FolderByKind(demo.OutlookAccountID, models.FolderDrafts)
	p, _ := s.ListMessages(models.ListQuery{FolderID: drafts.ID})
	found := false
	for _, m := range p.Messages {
		if m.Subject == "Draft v3" {
			found = true
			full, _ := s.Message(m.ID)
			if !full.IsDraft {
				t.Fatal("IsDraft not set")
			}
		}
		if m.Subject == "Draft v1" || m.Subject == "Draft v2" {
			t.Fatalf("stale draft %q", m.Subject)
		}
	}
	if !found {
		t.Fatal("coalesced draft not saved")
	}
	// Send the draft: it leaves Drafts and appears in Sent.
	req.To = []models.Address{{Email: "x@example.com"}}
	req.DraftID = id
	if err := s.Send(req); err != nil {
		t.Fatal(err)
	}
	s.SyncNow(demo.OutlookAccountID)
	p, _ = s.ListMessages(models.ListQuery{FolderID: drafts.ID})
	for _, m := range p.Messages {
		if m.Subject == "Draft v3" {
			t.Fatal("draft still there after sending")
		}
	}
	res, _ := s.SearchMessages(models.SearchQuery{Query: `in:sent subject:"Draft v3"`})
	if len(res) != 1 {
		t.Fatalf("sent draft %d", len(res))
	}
	// Deleting by draft message id.
	id, _ = s.SaveDraft(models.ComposeRequest{AccountID: demo.GmailAccountID, Subject: "Trash me"}, "")
	s.SyncNow(demo.GmailAccountID)
	res, _ = s.SearchMessages(models.SearchQuery{Query: `subject:"Trash me"`})
	if len(res) != 1 {
		t.Fatal("draft not synced")
	}
	if err := s.DeleteDraft(res[0].Message.ID); err != nil {
		t.Fatal(err)
	}
	res, _ = s.SearchMessages(models.SearchQuery{Query: `subject:"Trash me"`})
	if len(res) != 0 {
		t.Fatal("draft not deleted locally")
	}
}

func TestForwardKeepsAttachmentsAndPreview(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	res, _ := s.SearchMessages(models.SearchQuery{Query: "booking confirmation", AccountID: demo.GmailAccountID})
	if len(res) == 0 {
		t.Fatal("not found")
	}
	m, err := s.Message(res[0].Message.ID)
	if err != nil || len(m.Attachments) == 0 {
		t.Fatal(err)
	}
	data, err := s.AttachmentData(m.ID, m.Attachments[0].ID)
	if err != nil || data.MimeType != "application/pdf" || data.DataBase64 == "" {
		t.Fatalf("%+v %v", data.MimeType, err)
	}
	files, _ := filepath.Glob(filepath.Join(s.opts.DataDir, "attachments", demo.GmailAccountID, "*"))
	if len(files) != 1 {
		t.Fatalf("cache files %v", files)
	}
	err = s.Send(models.ComposeRequest{AccountID: demo.GmailAccountID, To: []models.Address{{Email: "f@example.com"}},
		BodyHTML: "<p>FYI uniquefwdword</p>", ReplyToID: m.ID, Mode: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	s.SyncNow(demo.GmailAccountID)
	fw, _ := s.SearchMessages(models.SearchQuery{Query: "uniquefwdword"})
	if len(fw) != 1 || !fw[0].Message.HasAttachments || !strings.HasPrefix(fw[0].Message.Subject, "Fwd: ") {
		t.Fatalf("forward %+v", fw)
	}
}

type recordingTransport struct{ reqs []*http.Request }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
}

func TestUnsubscribeOneClick(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	rt := &recordingTransport{}
	old := unsubscribeClient.Transport
	unsubscribeClient.Transport = rt
	defer func() { unsubscribeClient.Transport = old }()
	res, _ := s.SearchMessages(models.SearchQuery{Query: "morning brew"})
	m, _ := s.Message(res[0].Message.ID)
	if m.Unsubscribe == nil || !m.Unsubscribe.OneClick || m.Unsubscribe.Mailto == "" {
		t.Fatalf("unsubscribe %+v", m.Unsubscribe)
	}
	r, err := s.Unsubscribe(m.ID)
	if err != nil || r.Method != "oneClick" || len(rt.reqs) != 1 || rt.reqs[0].Method != "POST" {
		t.Fatalf("%+v %v", r, err)
	}
	body, _ := io.ReadAll(rt.reqs[0].Body)
	if string(body) != "List-Unsubscribe=One-Click" {
		t.Fatal(string(body))
	}
	plain, _ := s.SearchMessages(models.SearchQuery{Query: "sunday dinner"})
	if _, err := s.Unsubscribe(plain[0].Message.ID); err == nil {
		t.Fatal("expected no-unsubscribe error")
	}
}

// brokenProvider fails Folders with a configurable error.
type brokenProvider struct {
	provider.Provider
	mu  sync.Mutex
	bad error
}

func (b *brokenProvider) set(err error) { b.mu.Lock(); b.bad = err; b.mu.Unlock() }

func (b *brokenProvider) Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error) {
	b.mu.Lock()
	err := b.bad
	b.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return b.Provider.Folders(ctx)
}

func TestReauthBackoffOffline(t *testing.T) {
	ui := &fakeUI{}
	online := atomic.Bool{}
	online.Store(true)
	var bp *brokenProvider
	s, err := New(Options{DataDir: t.TempDir(), Demo: true, UI: ui, Secrets: secrets.NewMemory(),
		Online: func(context.Context) bool { return online.Load() },
		ProviderFactory: func(acct models.Account) (provider.Provider, error) {
			d := demo.New(acct)
			d.Latency, d.NewMailChance = 0, 0
			if acct.ID == demo.GmailAccountID {
				bp = &brokenProvider{Provider: d}
				return bp, nil
			}
			return d, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if ui.count(models.EventSyncProgress) == 0 {
		t.Fatal("no sync:progress during the first sync")
	}
	// Revoked sign-in.
	bp.set(errors.Join(auth.ErrReauthRequired, errors.New("invalid_grant")))
	err = s.SyncNow(demo.GmailAccountID)
	if err == nil || !strings.Contains(err.Error(), "Sign in again") {
		t.Fatalf("reauth error: %v", err)
	}
	a, _ := s.store.Account(demo.GmailAccountID)
	if !a.NeedsReauth || a.SyncError == "" {
		t.Fatalf("account %+v", a)
	}
	bp.set(errors.New("boom"))
	bp.set(nil)
	if _, err := s.ReauthAccount(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	a, _ = s.store.Account(demo.GmailAccountID)
	if a.NeedsReauth {
		t.Fatal("still needs reauth")
	}
	// Offline: network errors don't count as account failures.
	online.Store(false)
	bp.set(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")})
	err = s.SyncNow(demo.GmailAccountID)
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline: %v", err)
	}
	if st, ok := ui.last(models.EventNetworkStatus).(models.NetworkStatus); !ok || st.Online {
		t.Fatal("no offline event")
	}
	a, _ = s.store.Account(demo.GmailAccountID)
	if a.SyncError != "" {
		t.Fatalf("offline recorded as account error: %q", a.SyncError)
	}
	if err := s.SyncNow(demo.OutlookAccountID); err == nil {
		t.Fatal("sync while offline should report offline")
	}
	online.Store(true)
	bp.set(nil)
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if st, _ := ui.last(models.EventNetworkStatus).(models.NetworkStatus); !st.Online {
		t.Fatal("no back-online event")
	}
}

func TestServerSearchAndLoadOlder(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	res, _ := s.SearchMessages(models.SearchQuery{Query: "lasagna", AccountID: demo.OutlookAccountID})
	if len(res) == 0 {
		t.Fatal("seed not found")
	}
	// Drop it locally, as if it were older than the cache window.
	s.store.ApplySync(demo.OutlookAccountID, mail.SyncResult{Deleted: []string{res[0].Message.RemoteID}}, nil)
	res, _ = s.SearchMessages(models.SearchQuery{Query: "sunday dinner", AccountID: demo.OutlookAccountID})
	_ = res
	deadline := time.Now().Add(5 * time.Second)
	for ui := e.ui; ; {
		if ev, ok := ui.last(models.EventSearchUpdated).(models.SearchUpdated); ok && ev.Added > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no search:updated")
		}
		time.Sleep(20 * time.Millisecond)
	}
	res, _ = s.SearchMessages(models.SearchQuery{Query: "sunday dinner", AccountID: demo.OutlookAccountID})
	if len(res) == 0 {
		t.Fatal("server result not cached")
	}
	q := models.ListQuery{AccountID: demo.OutlookAccountID, FolderKind: models.FolderInbox, Limit: 200}
	n, err := s.LoadOlder(q)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	p, _ := s.ListMessages(q)
	if p.HasMoreRemote {
		t.Fatal("hasMoreRemote after the server ran out")
	}
	if _, err := os.Stat(s.opts.DataDir); err != nil {
		t.Fatal(err)
	}
	_ = store.ConversationPrefix
}

func TestContactsSignatureAndFolderOps(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	cs, err := s.SearchContacts("pri", 5)
	if err != nil || len(cs) == 0 || cs[0].Email != "priya.raman@northwind.io" || cs[0].Name != "Priya Raman" || cs[0].Count < 2 {
		t.Fatalf("contacts %+v %v", cs, err)
	}
	cs, _ = s.SearchContacts("raman", 5) // word start in the name
	if len(cs) == 0 || cs[0].Email != "priya.raman@northwind.io" {
		t.Fatalf("name word match %+v", cs)
	}
	cs, _ = s.SearchContacts("alex.demo", 5)
	if len(cs) != 0 {
		t.Fatalf("own address suggested: %+v", cs)
	}
	if cs, _ := s.SearchContacts("%", 5); len(cs) != 0 {
		t.Fatal("LIKE wildcard not escaped")
	}

	if err := s.UpdateAccountSignature(demo.GmailAccountID, `<p>Alex <b>Demo</b></p><script>alert(1)</script>`); err != nil {
		t.Fatal(err)
	}
	a, _ := s.store.Account(demo.GmailAccountID)
	if !strings.Contains(a.Signature, "<b>Demo</b>") || strings.Contains(a.Signature, "script") {
		t.Fatalf("signature %q", a.Signature)
	}

	inbox, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderInbox)
	if err := s.MarkFolderRead(inbox.ID); err != nil {
		t.Fatal(err)
	}
	s.SyncNow(demo.GmailAccountID)
	p, _ := s.ListMessages(models.ListQuery{FolderID: inbox.ID, UnreadOnly: true})
	if p.Total != 0 {
		t.Fatalf("unread left: %d", p.Total)
	}
	if err := s.EmptyFolder(inbox.ID); err == nil {
		t.Fatal("emptying the inbox must be refused")
	}
	trash, _ := s.store.FolderByKind(demo.OutlookAccountID, models.FolderTrash)
	if n, _ := s.store.LocalFolderCount(trash.ID); n == 0 {
		t.Fatal("demo trash is empty")
	}
	if err := s.EmptyFolder(trash.ID); err != nil {
		t.Fatal(err)
	}
	s.SyncNow(demo.OutlookAccountID)
	if n, _ := s.store.LocalFolderCount(trash.ID); n != 0 {
		t.Fatalf("trash not empty: %d", n)
	}
}

func TestRemoteContentBlocking(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	res, _ := s.SearchMessages(models.SearchQuery{Query: "morning brew"})
	m, err := s.Message(res[0].Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasRemoteContent || !m.RemoteContentBlocked || strings.Contains(m.BodyHTML, "open-pixel") {
		t.Fatalf("not blocked: remote=%v blocked=%v", m.HasRemoteContent, m.RemoteContentBlocked)
	}
	full, _ := s.MessageWithRemoteContent(m.ID)
	if !strings.Contains(full.BodyHTML, "open-pixel") || full.RemoteContentBlocked {
		t.Fatal("remote content not restored")
	}
	st := s.GetSettings()
	st.ShowRemoteImages = true
	s.SaveSettings(st)
	if m, _ := s.Message(m.ID); m.RemoteContentBlocked {
		t.Fatal("blocked although showRemoteImages is on")
	}
}
