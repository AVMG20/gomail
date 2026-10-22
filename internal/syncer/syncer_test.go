package syncer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/store"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (r *recorder) Emit(event string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.data = append(r.data, data)
}

func (r *recorder) last(event string) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i] == event {
			return r.data[i]
		}
	}
	return nil
}

func (r *recorder) count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == event {
			n++
		}
	}
	return n
}

// flaky wraps the demo provider; fail makes the named operations fail.
type flaky struct {
	*demo.Provider
	mu     sync.Mutex
	fail   map[string]error
	calls  map[string]int
	labels []string
	// markFolderReadHook, if set, runs before MarkFolderRead (tests block it).
	markFolderReadHook func()
	// moveHook, if set, runs before Move; an error fails the move.
	moveHook func(target mail.Folder) error
	// labelHook, if set, runs before SetLabel.
	labelHook func()
	// onEmpty runs at the start of EmptyFolder (while the local change is
	// pending).
	onEmpty func()
}

func (f *flaky) err(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	return f.fail[op]
}

func (f *flaky) set(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[op] = err
}

func (f *flaky) Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error) {
	if err := f.err("folders"); err != nil {
		return nil, nil, err
	}
	return f.Provider.Folders(ctx)
}

func (f *flaky) SetRead(ctx context.Context, ids []string, read bool) error {
	if err := f.err("read"); err != nil {
		return err
	}
	return f.Provider.SetRead(ctx, ids, read)
}

func (f *flaky) SetStarred(ctx context.Context, ids []string, starred bool) error {
	if err := f.err("star"); err != nil {
		return err
	}
	return f.Provider.SetStarred(ctx, ids, starred)
}

func (f *flaky) Move(ctx context.Context, ids []string, target mail.Folder) error {
	if err := f.err("move"); err != nil {
		return err
	}
	if f.moveHook != nil {
		if err := f.moveHook(target); err != nil {
			return err
		}
	}
	return f.Provider.Move(ctx, ids, target)
}

func (f *flaky) SetLabel(ctx context.Context, ids []string, name string, add bool) error {
	f.mu.Lock()
	f.labels = append(f.labels, fmt.Sprintf("%s:%v:%d", name, add, len(ids)))
	f.mu.Unlock()
	if f.labelHook != nil {
		f.labelHook()
	}
	return f.Provider.SetLabel(ctx, ids, name, add)
}

func (f *flaky) ListOlder(ctx context.Context, folder mail.Folder, before time.Time, limit int) ([]mail.Message, error) {
	if err := f.err("older"); err != nil {
		return nil, err
	}
	return f.Provider.ListOlder(ctx, folder, before, limit)
}

func (f *flaky) MarkFolderRead(ctx context.Context, folder mail.Folder) error {
	if err := f.err("folderread"); err != nil {
		return err
	}
	if f.markFolderReadHook != nil {
		f.markFolderReadHook()
	}
	return f.Provider.MarkFolderRead(ctx, folder)
}

func (f *flaky) EmptyFolder(ctx context.Context, folder mail.Folder) error {
	if f.onEmpty != nil {
		f.onEmpty()
	}
	if err := f.err("empty"); err != nil {
		return err
	}
	return f.Provider.EmptyFolder(ctx, folder)
}

type testEnv struct {
	st     *store.Store
	e      *Engine
	rec    *recorder
	provs  map[string]*flaky
	online bool
	newIDs map[string]int
	mu     sync.Mutex
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	te := &testEnv{st: st, rec: &recorder{}, provs: map[string]*flaky{}, online: true, newIDs: map[string]int{}}
	for _, a := range demo.Accounts() {
		a.CreatedAt = time.Now()
		if err := st.InsertAccount(a); err != nil {
			t.Fatal(err)
		}
		d := demo.NewWithOlder(a, 30, 20)
		d.Latency, d.NewMailChance = 0, 0
		te.provs[a.ID] = &flaky{Provider: d, fail: map[string]error{}, calls: map[string]int{}}
	}
	te.e = New(Config{
		Store: st,
		Emit:  te.rec,
		Provider: func(id string) (provider.Provider, error) {
			p, ok := te.provs[id]
			if !ok {
				return nil, errors.New("no provider")
			}
			return p, nil
		},
		ResolveLabels: func(_ string, labels []mail.Label) map[string]string {
			out := map[string]string{}
			for _, l := range labels {
				out[strings.ToLower(l.Name)] = strings.ToLower(l.Name)
			}
			return out
		},
		Interval: func(string) time.Duration { return time.Hour },
		OnNewMessages: func(acct string, ids []string, initial bool) <-chan struct{} {
			te.mu.Lock()
			te.newIDs[acct] += len(ids)
			te.mu.Unlock()
			return nil
		},
		Online: func(context.Context) bool {
			te.mu.Lock()
			defer te.mu.Unlock()
			return te.online
		},
		Backfill: func(string) (int, bool) { return 0, false },
	})
	t.Cleanup(func() { te.e.Stop(); st.Close() })
	return te
}

func (te *testEnv) inbox(t *testing.T, acct string, unreadOnly bool) []models.MessageSummary {
	t.Helper()
	f, err := te.st.FolderByKind(acct, models.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	page, err := te.st.ListMessages(models.ListQuery{FolderID: f.ID, UnreadOnly: unreadOnly, Limit: 500}, store.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return page.Messages
}

func TestSyncAnnounceAndIngest(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	if err := te.e.SyncNow(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if len(te.inbox(t, demo.GmailAccountID, false)) == 0 || len(te.inbox(t, demo.OutlookAccountID, false)) == 0 {
		t.Fatal("initial sync stored nothing")
	}
	if te.rec.count(models.EventNewMail) != 0 {
		t.Fatal("initial sync announced mail")
	}
	if te.rec.last(models.EventSyncProgress) == nil {
		t.Fatal("no progress during the initial sync")
	}
	acct, _ := te.st.Account(demo.GmailAccountID)
	if acct.LastSyncAt == nil || acct.SyncError != "" || te.e.Syncing(acct.ID) {
		t.Fatalf("account after sync %+v", acct)
	}
	// Provider labels seen during the sync.
	if !te.e.HasProviderLabel(demo.GmailAccountID, "finance") {
		t.Fatal("provider label not remembered")
	}

	te.provs[demo.GmailAccountID].Deliver("Hello there", "friend@example.com")
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	ev, ok := te.rec.last(models.EventNewMail).(models.NewMailEvent)
	if !ok || ev.Count != 1 || ev.Latest.Subject != "Hello there" || len(ev.Senders) != 1 {
		t.Fatalf("new mail %+v", ev)
	}
	if fin, ok := te.rec.last(models.EventSyncFinished).(models.SyncEvent); !ok || fin.NewMessages != 1 || fin.Error != "" {
		t.Fatalf("finished %+v", fin)
	}

	// Mail Gmail or Outlook filed as spam/junk is stored but never announced.
	before := te.rec.count(models.EventNewMail)
	te.provs[demo.GmailAccountID].DeliverTo(models.FolderSpam, "You won a prize", "spam@example.com")
	te.provs[demo.OutlookAccountID].DeliverTo(models.FolderSpam, "Cheap watches", "junk@example.com")
	if err := te.e.SyncNow(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if n := te.rec.count(models.EventNewMail); n != before {
		t.Fatalf("spam announced as new mail (%d events)", n-before)
	}

	// Ingest (older pages) never announces.
	n, err := te.e.Ingest(demo.GmailAccountID, []mail.Message{{RemoteID: "ingested-1", ThreadID: "ti", Subject: "Old",
		Date: time.Now().Add(-400 * 24 * time.Hour), FolderRemoteIDs: []string{"INBOX"}, Labels: []string{"Travel"}}}, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("ingest %d %v", n, err)
	}
	if n, _ := te.e.Ingest(demo.GmailAccountID, nil, time.Now()); n != 0 {
		t.Fatal("empty ingest")
	}
	if te.e.SyncNow(ctx, "nope") == nil {
		t.Fatal("unknown account must fail")
	}
}

func TestOptimisticActionsRevert(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	unread := te.inbox(t, demo.GmailAccountID, true)
	if len(unread) == 0 {
		t.Fatal("no unread mail")
	}
	id := unread[0].ID

	if err := te.e.MarkRead([]string{id}, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := te.st.Summaries([]string{id}); s[0].Unread {
		t.Fatal("not marked read")
	}
	// Server refuses: the change is reverted with a readable error.
	te.provs[demo.GmailAccountID].set("read", &httpx.Error{Service: "Gmail", Status: 500, Code: "backendError", Message: "boom"})
	err := te.e.MarkRead([]string{id}, false)
	if err == nil || !strings.Contains(err.Error(), "Couldn't mark messages as unread") {
		t.Fatalf("expected revert error, got %v", err)
	}
	if s, _ := te.st.Summaries([]string{id}); s[0].Unread {
		t.Fatal("failed change not reverted")
	}
	// No-op changes don't hit the server.
	before := te.provs[demo.GmailAccountID].calls["read"]
	if err := te.e.MarkRead([]string{id}, true); err != nil || te.provs[demo.GmailAccountID].calls["read"] != before {
		t.Fatalf("no-op pushed: %v", err)
	}

	if err := te.e.SetStarred([]string{id}, true); err != nil {
		t.Fatal(err)
	}
	if s, _ := te.st.Summaries([]string{id}); !s[0].Starred {
		t.Fatal("not starred")
	}
	te.provs[demo.GmailAccountID].set("star", errors.New("nope"))
	if err := te.e.SetStarred([]string{id}, false); err == nil {
		t.Fatal("expected star error")
	}
	if s, _ := te.st.Summaries([]string{id}); !s[0].Starred {
		t.Fatal("star not reverted")
	}

	// Archive, then a failed move back is reverted.
	if err := te.e.MoveToKind([]string{id}, models.FolderArchive); err != nil {
		t.Fatal(err)
	}
	inbox, _ := te.st.FolderByKind(demo.GmailAccountID, models.FolderInbox)
	if s, _ := te.st.Summaries([]string{id}); contains(s[0].FolderIDs, inbox.ID) {
		t.Fatal("still in inbox after archive")
	}
	te.provs[demo.GmailAccountID].set("move", errors.New("denied"))
	if err := te.e.MoveToFolder([]string{id}, inbox.ID); err == nil {
		t.Fatal("expected move error")
	}
	if s, _ := te.st.Summaries([]string{id}); contains(s[0].FolderIDs, inbox.ID) {
		t.Fatal("failed move not reverted")
	}
	te.provs[demo.GmailAccountID].set("move", nil)
	if err := te.e.MoveToFolder([]string{id}, inbox.ID); err != nil {
		t.Fatal(err)
	}
	other, _ := te.st.FolderByKind(demo.OutlookAccountID, models.FolderInbox)
	if err := te.e.MoveToFolder([]string{id}, other.ID); err == nil {
		t.Fatal("cross-account move allowed")
	}
	if err := te.e.MoveToFolder([]string{id}, "missing"); err == nil {
		t.Fatal("move to missing folder allowed")
	}
	if err := te.e.MarkRead(make([]string, MaxBulk+1), true); err == nil {
		t.Fatal("bulk limit not enforced")
	}

	// Provider labels are pushed only where the provider knows them.
	te.e.PushLabel([]string{id}, "Finance", true)
	te.e.PushLabel([]string{id}, "made-up", true)
	te.e.wg.Wait()
	if l := te.provs[demo.GmailAccountID].labels; len(l) != 1 || l[0] != "Finance:true:1" {
		t.Fatalf("pushed labels %v", l)
	}
}

func TestFolderOperations(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	inbox, _ := te.st.FolderByKind(demo.OutlookAccountID, models.FolderInbox)
	if len(te.inbox(t, demo.OutlookAccountID, true)) == 0 {
		t.Fatal("no unread mail")
	}
	p := te.provs[demo.OutlookAccountID]
	p.set("folderread", errors.New("server down"))
	if err := te.e.MarkFolderRead(inbox.ID); err == nil || !strings.Contains(err.Error(), "couldn't mark") {
		t.Fatalf("expected error, got %v", err)
	}
	if len(te.inbox(t, demo.OutlookAccountID, true)) == 0 {
		t.Fatal("failed mark-folder-read not reverted")
	}
	p.set("folderread", nil)
	if err := te.e.MarkFolderRead(inbox.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(te.inbox(t, demo.OutlookAccountID, true)); n != 0 {
		t.Fatalf("%d unread left", n)
	}
	if err := te.e.EmptyFolder(inbox.ID); err == nil {
		t.Fatal("inbox can't be emptied")
	}
	trash, _ := te.st.FolderByKind(demo.OutlookAccountID, models.FolderTrash)
	ids, _ := te.st.FolderMessageIDs(trash.ID, false)
	if len(ids) == 0 {
		t.Fatal("demo trash is empty")
	}
	p.set("empty", errors.New("nope"))
	if err := te.e.EmptyFolder(trash.ID); err == nil {
		t.Fatal("expected empty error")
	}
	if again, _ := te.st.FolderMessageIDs(trash.ID, false); len(again) != len(ids) {
		t.Fatal("failed empty not reverted")
	}
	p.set("empty", nil)
	if err := te.e.EmptyFolder(trash.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := te.st.FolderMessageIDs(trash.ID, false); len(again) != 0 {
		t.Fatalf("%d left in trash", len(again))
	}
	if sums, _ := te.st.Summaries(ids); len(sums) != 0 {
		t.Fatal("emptied messages still cached")
	}
	if err := te.e.MarkFolderRead("missing"); err == nil {
		t.Fatal("missing folder")
	}
}

func TestReauthOfflineAndBackoff(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	p := te.provs[demo.GmailAccountID]
	p.set("folders", fmt.Errorf("%w for x: please sign in again", auth.ErrReauthRequired))
	err := te.e.SyncNow(ctx, demo.GmailAccountID)
	if err == nil || !strings.Contains(err.Error(), "Sign in again") {
		t.Fatalf("reauth error %v", err)
	}
	acct, _ := te.st.Account(demo.GmailAccountID)
	if !acct.NeedsReauth || acct.SyncError == "" {
		t.Fatalf("account %+v", acct)
	}
	// Periodic syncs skip accounts waiting for sign-in.
	calls := p.calls["folders"]
	te.e.syncDue(true)
	te.e.wg.Wait()
	if p.calls["folders"] != calls {
		t.Fatal("synced an account that needs re-authentication")
	}
	p.set("folders", nil)
	te.e.ResetBackoff(demo.GmailAccountID)
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if acct, _ := te.st.Account(demo.GmailAccountID); acct.NeedsReauth {
		t.Fatal("reauth flag not cleared by a successful sync")
	}

	// Network failure while the probe says offline: offline status, no
	// error on the account.
	p.set("folders", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})
	te.mu.Lock()
	te.online = false
	te.mu.Unlock()
	err = te.e.SyncNow(ctx, demo.GmailAccountID)
	if err == nil || !strings.Contains(err.Error(), "offline") || !te.e.Offline() {
		t.Fatalf("offline: %v %v", err, te.e.Offline())
	}
	if ns, ok := te.rec.last(models.EventNetworkStatus).(models.NetworkStatus); !ok || ns.Online {
		t.Fatalf("network status %+v", ns)
	}
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("sync while offline: %v", err)
	}
	te.mu.Lock()
	te.online = true
	te.mu.Unlock()
	p.set("folders", nil)
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil || te.e.Offline() {
		t.Fatalf("back online: %v", err)
	}
	if ns, _ := te.rec.last(models.EventNetworkStatus).(models.NetworkStatus); !ns.Online {
		t.Fatal("no online event")
	}

	// Server errors count as failures and back off.
	p.set("folders", &httpx.Error{Service: "Gmail", Status: 503, Code: "", Message: "unavailable"})
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("503: %v", err)
	}
	st := te.e.state(demo.GmailAccountID)
	if st.failures != 1 {
		t.Fatalf("failures %d", st.failures)
	}
	te.e.Forget(demo.GmailAccountID)
	if te.e.state(demo.GmailAccountID).failures != 0 {
		t.Fatal("forget kept state")
	}
	for n, want := range map[int]time.Duration{0: 0, 1: 30 * time.Second, 2: time.Minute, 4: 4 * time.Minute, 20: 30 * time.Minute} {
		if got := backoff(n); got != want {
			t.Fatalf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestStartWakeStopAndBackfill(t *testing.T) {
	te := newTestEnv(t)
	old := backfillDelay
	backfillDelay = time.Hour
	defer func() { backfillDelay = old }()
	te.e.Start()
	deadline := time.Now().Add(10 * time.Second)
	for te.rec.count(models.EventSyncFinished) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("start did not sync both accounts")
		}
		time.Sleep(10 * time.Millisecond)
	}
	te.e.Wake()
	te.e.Wake() // coalesced
	steps := 0
	for te.e.BackfillStep(context.Background()) {
		steps++
		if steps > 100 {
			t.Fatal("backfill never finishes")
		}
	}
	if steps == 0 {
		t.Fatal("backfill did nothing")
	}
	te.e.Stop()
	if err := te.e.SyncNow(context.Background(), demo.GmailAccountID); err == nil {
		t.Fatal("sync after stop")
	}
}

func TestBackfillFolders(t *testing.T) {
	folders := []models.Folder{{ID: "i", Kind: models.FolderInbox}, {ID: "a", Kind: models.FolderArchive}, {ID: "s", Kind: models.FolderSent},
		{ID: "t", Kind: models.FolderTrash}, {ID: "c", Kind: models.FolderCustom}, {ID: "d", Kind: models.FolderDrafts}}
	if got := backfillFolders(models.Account{Provider: models.ProviderGmail}, folders); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("gmail %v", got)
	}
	if got := backfillFolders(models.Account{Provider: models.ProviderOutlook}, folders); len(got) != 4 {
		t.Fatalf("outlook %v", got)
	}
}

func TestHumanErrorsAndHelpers(t *testing.T) {
	g := models.Account{Provider: models.ProviderGmail, Email: "a@b.c"}
	o := models.Account{Provider: models.ProviderOutlook}
	cases := []struct {
		acct models.Account
		err  error
		want string
	}{
		{g, nil, ""},
		{g, auth.ErrReauthRequired, "Sign in again"},
		{g, &httpx.Error{Service: "Gmail", Status: 401, Code: "", Message: ""}, "Sign in again"},
		{g, context.Canceled, "cancelled"},
		{o, context.DeadlineExceeded, "Outlook took too long"},
		{g, &net.DNSError{Err: "no such host", Name: "x"}, "Can't reach Gmail"},
		{o, &httpx.Error{Service: "Graph", Status: 429, Code: "", Message: ""}, "limiting requests"},
		{g, errors.New("plain"), "plain"},
	}
	for _, c := range cases {
		if got := HumanError(c.acct, c.err); !strings.Contains(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("HumanError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	if IsNetworkError(nil) || IsNetworkError(errors.New("x")) || !IsNetworkError(errors.New("could not reach Gmail: eof")) {
		t.Fatal("IsNetworkError")
	}
	if got := dedupe([]string{"a", "a", "b"}); got != "a; b" {
		t.Fatal(got)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !ProbeHosts("127.0.0.1:1", addr)(context.Background()) {
		t.Fatal("probe failed for a listening port")
	}
	ln.Close()
	if ProbeHosts(addr)(context.Background()) {
		t.Fatal("probe succeeded for a closed port")
	}
}

func TestHumanErrorUnknownService(t *testing.T) {
	if got := HumanError(models.Account{}, &httpx.Error{Status: 500}); !strings.Contains(got, "the mail server") {
		t.Fatal(got)
	}
}

// TestMergeCancelDoneParent: a done parent cancels synchronously, so no
// work starts after Stop (this used to race and was the flaky
// "sync after stop" failure).
func TestMergeCancelDoneParent(t *testing.T) {
	b, cancelB := context.WithCancel(context.Background())
	cancelB()
	for i := 0; i < 1000; i++ {
		ctx, cancel := mergeCancel(context.Background(), b)
		if ctx.Err() == nil {
			t.Fatal("merged context not cancelled right away")
		}
		cancel()
	}
	a, cancelA := context.WithCancel(context.Background())
	ctx, cancel := mergeCancel(a, context.Background())
	defer cancel()
	cancelA()
	if ctx.Err() == nil {
		t.Fatal("first parent not propagated")
	}
}

// TestQuietSyncSkipsMessagesChanged: a periodic sync that changes no mail
// doesn't make the UI re-fetch its list.
func TestQuietSyncSkipsMessagesChanged(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	before := te.rec.count(models.EventMessagesChanged)
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if n := te.rec.count(models.EventMessagesChanged) - before; n != 0 {
		t.Fatalf("quiet sync emitted messages:changed %d times", n)
	}
	if te.rec.count(models.EventSyncFinished) < 2 {
		t.Fatal("no sync:finished")
	}
	te.provs[demo.GmailAccountID].Deliver("Hello there", "friend@example.com")
	if err := te.e.SyncNow(ctx, demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if te.rec.count(models.EventMessagesChanged) == before {
		t.Fatal("sync with new mail emitted no messages:changed")
	}
}

// Each mailbox is checked on its own interval.
func TestPerAccountInterval(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	if err := te.e.SyncNow(ctx, ""); err != nil {
		t.Fatal(err)
	}
	te.e.cfg.Interval = func(id string) time.Duration {
		if id == demo.GmailAccountID {
			return 0 // due right away
		}
		return time.Hour
	}
	before := map[string]int{}
	for id, p := range te.provs {
		before[id] = p.calls["folders"]
	}
	te.e.syncDue(false)
	te.e.wg.Wait()
	for id, p := range te.provs {
		want := before[id]
		if id == demo.GmailAccountID {
			want++
		}
		if p.calls["folders"] != want {
			t.Errorf("%s synced %d times, want %d", id, p.calls["folders"]-before[id], want-before[id])
		}
	}
}
