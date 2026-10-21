package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
)

// draftProbe wraps the demo provider to hold draft saves and fail draft
// lookups on demand.
type draftProbe struct {
	*demo.Provider
	mu         sync.Mutex
	gate       chan struct{} // when set, SaveDraft waits for it
	started    chan struct{} // signalled when a gated save starts
	draftIDErr error
	saveErr    error    // when set, SaveDraft fails with it
	saved      []string // subjects of the saves that reached the server
}

func (p *draftProbe) SaveDraft(ctx context.Context, out mail.Outgoing, draftID string) (string, error) {
	p.mu.Lock()
	gate, started, saveErr := p.gate, p.started, p.saveErr
	p.mu.Unlock()
	if gate != nil {
		started <- struct{}{}
		<-gate
	}
	if saveErr != nil {
		return "", saveErr
	}
	p.mu.Lock()
	p.saved = append(p.saved, out.Subject)
	p.mu.Unlock()
	return p.Provider.SaveDraft(ctx, out, draftID)
}

func (p *draftProbe) DraftID(ctx context.Context, rid string) (string, error) {
	p.mu.Lock()
	err := p.draftIDErr
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	return p.Provider.DraftID(ctx, rid)
}

func newProbeEnv(t *testing.T) (*Service, map[string]*draftProbe) {
	t.Helper()
	probes := map[string]*draftProbe{}
	var mu sync.Mutex
	s, err := New(Options{
		DataDir: t.TempDir(), Version: "test", Demo: true, UI: &fakeUI{savePath: filepath.Join(t.TempDir(), "out.bin")}, Secrets: secrets.NewMemory(),
		ProviderFactory: func(acct models.Account) (provider.Provider, error) {
			mu.Lock()
			defer mu.Unlock()
			d := demo.New(acct)
			d.Latency, d.NewMailChance = 0, 0
			p := &draftProbe{Provider: d}
			probes[acct.ID] = p
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	return s, probes
}

// draftsWith counts server drafts whose subject starts with prefix.
func draftsWith(t *testing.T, s *Service, acct, prefix string) int {
	t.Helper()
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	f, _ := s.store.FolderByKind(acct, models.FolderDrafts)
	p, err := s.ListMessages(models.ListQuery{FolderID: f.ID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range p.Messages {
		if strings.HasPrefix(m.Subject, prefix) {
			n++
		}
	}
	return n
}

// An autosave in flight when the draft is discarded must not recreate it.
func TestDiscardWaitsForAutosaveInFlight(t *testing.T) {
	old := draftCoalesce
	draftCoalesce = 150 * time.Millisecond
	defer func() { draftCoalesce = old }()
	s, probes := newProbeEnv(t)
	acct := demo.GmailAccountID
	req := models.ComposeRequest{AccountID: acct, Subject: "Ghost v1"}
	h, err := s.SaveDraft(req, "")
	if err != nil {
		t.Fatal(err)
	}
	p := probes[acct]
	p.mu.Lock()
	p.gate, p.started = make(chan struct{}), make(chan struct{}, 1)
	gate := p.gate
	p.mu.Unlock()
	req.Subject = "Ghost v2"
	if _, err := s.SaveDraft(req, h); err != nil { // coalesced: flushed by the timer
		t.Fatal(err)
	}
	<-p.started // the flush is now in flight
	p.mu.Lock()
	p.gate = nil
	p.mu.Unlock()
	deleted := make(chan error, 1)
	go func() { deleted <- s.DeleteDraft(h) }()
	time.Sleep(100 * time.Millisecond)
	close(gate)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := draftsWith(t, s, acct, "Ghost"); n != 0 {
		t.Fatalf("%d drafts after discarding", n)
	}
}

// A transient draft lookup failure must neither create a second draft nor
// send while leaving the draft behind.
func TestDraftLookupFailureIsNotMissingDraft(t *testing.T) {
	s, probes := newProbeEnv(t)
	acct := demo.GmailAccountID
	req := models.ComposeRequest{AccountID: acct, Subject: "Lookup v1"}
	if _, err := s.SaveDraft(req, ""); err != nil {
		t.Fatal(err)
	}
	if n := draftsWith(t, s, acct, "Lookup"); n != 1 {
		t.Fatalf("setup: %d drafts", n)
	}
	f, _ := s.store.FolderByKind(acct, models.FolderDrafts)
	page, _ := s.ListMessages(models.ListQuery{FolderID: f.ID, Limit: 200})
	var localID string
	for _, m := range page.Messages {
		if strings.HasPrefix(m.Subject, "Lookup") {
			localID = m.ID
		}
	}
	probes[acct].mu.Lock()
	probes[acct].draftIDErr = errors.New("connection reset")
	probes[acct].mu.Unlock()
	req.Subject = "Lookup v2"
	if _, err := s.SaveDraft(req, localID); err == nil {
		t.Fatal("save succeeded although the draft couldn't be looked up")
	}
	req.To = []models.Address{{Email: "x@example.com"}}
	req.DraftID = localID
	if err := s.Send(req); err == nil {
		t.Fatal("sent although the draft couldn't be looked up")
	}
	probes[acct].mu.Lock()
	probes[acct].draftIDErr = nil
	probes[acct].mu.Unlock()
	if n := draftsWith(t, s, acct, "Lookup"); n != 1 {
		t.Fatalf("%d drafts after failed lookups", n)
	}
	// A draft that really is gone is saved as a new one.
	probes[acct].mu.Lock()
	probes[acct].draftIDErr = provider.ErrDraftGone
	probes[acct].mu.Unlock()
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Lookup v3"}, localID); err != nil {
		t.Fatal(err)
	}
}

// After a draft is recreated (deleted elsewhere), later saves with the old
// handle update the new draft instead of creating one each time.
func TestRecreatedDraftIsFollowed(t *testing.T) {
	old := draftCoalesce
	draftCoalesce = 100 * time.Millisecond
	defer func() { draftCoalesce = old }()
	s, probes := newProbeEnv(t)
	acct := demo.OutlookAccountID
	h, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Phoenix 1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	dh, _ := parseDraftHandle(h)
	if err := probes[acct].Provider.DeleteDraft(context.Background(), dh.remoteID); err != nil { // deleted on another device
		t.Fatal(err)
	}
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Phoenix 2"}, h); err != nil { // coalesced
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // flushed: recreated
	for i := 3; i <= 4; i++ {
		if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: fmt.Sprint("Phoenix ", i)}, h); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if n := draftsWith(t, s, acct, "Phoenix"); n != 1 {
		t.Fatalf("%d drafts, want 1", n)
	}
}

func TestMergeIDs(t *testing.T) {
	if got := mergeIDs([]string{"n1", "n2"}, []string{"o1", "n2", "o2"}, 4); fmt.Sprint(got) != "[n1 n2 o1 o2]" {
		t.Fatal(got)
	}
	if got := mergeIDs([]string{"n1"}, []string{"o1", "o2"}, 2); fmt.Sprint(got) != "[n1 o1]" {
		t.Fatal(got)
	}
}

// Messages already scored with the current label set are not sent to the
// model again, but a changed threshold applies to their stored scores.
func TestThresholdChangeAppliesToStoredScores(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	page, _ := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox, Limit: 1})
	id := page.Messages[0].ID
	ins, err := s.store.ClassifyInputs([]string{id})
	if err != nil || len(ins) != 1 {
		t.Fatal(err)
	}
	hash := aiHash(s.labelsetHash(ins[0].AccountID), ins[0])
	label := s.GetSettings().Labels[0].ID
	_ = s.store.DeleteUnknownLabels(nil) // start without any label assignments
	if _, _, err := s.store.ApplyAIScores(id, map[string]float64{label: 0.6}, store.AIPolicy{AddThreshold: 0.5, RemoveThreshold: 0.1}, hash); err != nil {
		t.Fatal(err)
	}
	st := s.GetSettings()
	st.ClassifyThreshold = 0.8
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	ins, _ = s.store.ClassifyInputs([]string{id})
	r := s.classifyOne(context.Background(), ins[0])
	if !r.Skipped || fmt.Sprint(r.Removed) != "["+label+"]" {
		t.Fatalf("result %+v", r)
	}
}

func TestAttachmentsCompleteReachesProvider(t *testing.T) {
	e := newEnv(t, "")
	_, out, err := e.s.buildOutgoing(models.ComposeRequest{AccountID: demo.OutlookAccountID, Subject: "x", AttachmentsComplete: true}, true)
	if err != nil || !out.AttachmentsComplete {
		t.Fatalf("%+v %v", out, err)
	}
}
