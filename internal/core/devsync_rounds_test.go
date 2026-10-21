package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/devsync"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/syncapi"
)

// connectPair turns on sync on a (new code) and joins b with it.
func connectPair(t *testing.T, url string, a, b *Service, secrets bool) string {
	t.Helper()
	if _, err := a.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Create: true, IncludeSecrets: secrets}); err != nil {
		t.Fatal(err)
	}
	code, err := a.DeviceSyncCode()
	if err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a)
	if _, err := b.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Code: code}); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, b, a, b)
	return code
}

// serverEntries decrypts a document on the server.
func serverEntries(t *testing.T, url, code, doc string) devsync.Entries {
	t.Helper()
	keys, _, _ := devsync.KeysFromCode(code)
	b, _ := json.Marshal(syncapi.LoginRequest{Account: keys.Account, AuthKey: base64.StdEncoding.EncodeToString(keys.Auth)})
	resp, err := http.Post(url+"/v1/login", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var login syncapi.LoginResponse
	_ = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	req, _ := http.NewRequest("GET", url+"/v1/docs/"+doc, nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return devsync.Entries{}
	}
	var d syncapi.Doc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	plain, err := devsync.Open(keys.Enc, doc, d.Data)
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Entries devsync.Entries }
	if err := json.Unmarshal(plain, &body); err != nil {
		t.Fatal(err)
	}
	return body.Entries
}

// A computer that joins an account whose sign-ins don't sync uploads none
// of its own, and leaves the others' OpenRouter key alone.
func TestDeviceSyncJoinerKeepsItsSecrets(t *testing.T) {
	url, _ := newSyncServer(t)
	a, c := newBlankService(t), newBlankService(t)
	addDemoAccount(t, a, 0, "a-gmail")
	if err := a.SetOpenRouterKey("sk-A"); err != nil {
		t.Fatal(err)
	}
	addDemoAccount(t, c, 1, "c-outlook")
	if err := c.SetOpenRouterKey("sk-C"); err != nil {
		t.Fatal(err)
	}
	code := connectPair(t, url, a, c, false)
	if c.DeviceSyncStatus().IncludeSecrets {
		t.Fatal("the joining computer turned on syncing sign-ins")
	}
	if k, _ := a.openRouterKey(); k != "sk-A" {
		t.Fatalf("a's key: %q", k)
	}
	if k, _ := c.openRouterKey(); k != "sk-C" {
		t.Fatalf("c's key: %q", k)
	}
	for k, e := range serverEntries(t, url, code, docAccounts) {
		if (strings.HasPrefix(k, "s/") || strings.HasPrefix(k, "t/")) && !e.D {
			t.Fatalf("a secret on the server: %s", k)
		}
	}
	// Where they sync, a joining computer takes the account's choice too.
	url2, _ := newSyncServer(t)
	d, e := newBlankService(t), newBlankService(t)
	if err := d.SetOpenRouterKey("sk-D"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetOpenRouterKey("sk-E"); err != nil {
		t.Fatal(err)
	}
	connectPair(t, url2, d, e, true)
	if !e.DeviceSyncStatus().IncludeSecrets {
		t.Fatal("the joining computer didn't take the account's choice")
	}
	if k, _ := e.openRouterKey(); k != "sk-D" {
		t.Fatalf("e's key: %q", k)
	}
}

// Clearing a key on one computer clears it on the others while sign-ins
// sync; it doesn't come back.
func TestDeviceSyncClearedKeyStaysCleared(t *testing.T) {
	url, _ := newSyncServer(t)
	a, b := newBlankService(t), newBlankService(t)
	if err := a.SetOpenRouterKey("sk-shared"); err != nil {
		t.Fatal(err)
	}
	st := a.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret = "id.apps.example.com", "secret"
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	connectPair(t, url, a, b, true)
	if k, _ := b.openRouterKey(); k != "sk-shared" || b.Settings().GoogleClientID == "" {
		t.Fatal("the keys didn't sync")
	}
	if err := a.SetOpenRouterKey(""); err != nil {
		t.Fatal(err)
	}
	st = a.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret = "", ""
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b, a, b, a)
	for name, s := range map[string]*Service{"a": a, "b": b} {
		if k, _ := s.openRouterKey(); k != "" {
			t.Fatalf("%s: the cleared key is back: %q", name, k)
		}
		if st := s.Settings(); st.GoogleClientID != "" || st.GoogleClientSecret != "" {
			t.Fatalf("%s: the cleared OAuth app is back: %+v", name, st.GoogleClientID)
		}
	}

	// Turning sign-ins off keeps every computer's key.
	if err := b.SetOpenRouterKey("sk-b"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, b, a)
	if err := a.SetDeviceSyncSecrets(false); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b, a, b)
	for name, s := range map[string]*Service{"a": a, "b": b} {
		if k, _ := s.openRouterKey(); k != "sk-b" {
			t.Fatalf("%s: key %q after turning sign-ins off", name, k)
		}
	}
}

// A joining computer keeps its own mailbox, even when the account removed
// that mailbox long ago.
func TestDeviceSyncJoinerKeepsMailboxRemovedBefore(t *testing.T) {
	url, _ := newSyncServer(t)
	a := newBlankService(t)
	addDemoAccount(t, a, 0, "a-gmail")
	if _, err := a.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Create: true, IncludeSecrets: true}); err != nil {
		t.Fatal(err)
	}
	code, _ := a.DeviceSyncCode()
	syncDevices(t, a)
	if err := a.RemoveAccount("a-gmail"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a)

	b := newBlankService(t)
	addDemoAccount(t, b, 0, "b-gmail")
	if _, err := b.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Code: code}); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, b, a, b)
	if _, err := b.store.Account("b-gmail"); err != nil {
		t.Fatal("the joining computer lost its mailbox")
	}
	if _, accts := a.mailboxes(); len(accts) != 1 {
		t.Fatalf("the other computer has %d mailboxes", len(accts))
	}
}

// Removing a mailbox deletes its sign-in and synced state on the server,
// so they don't come back when it is added again.
func TestDeviceSyncRemovedMailboxLeavesNothing(t *testing.T) {
	url, _ := newSyncServer(t)
	a, b := newBlankService(t), newBlankService(t)
	acct := addDemoAccount(t, a, 0, "a-gmail")
	mb := mailboxKey(acct)
	st := a.GetSettings()
	st.MutedMailboxes = []string{"a-gmail"}
	st.NotJunkSenders = map[string][]string{"a-gmail": {"friend@example.com"}}
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	inbox := firstInbox(t, a, "a-gmail")
	work := labelNamed(t, a, "a-gmail", "Finance")
	if _, err := a.AddLabel([]string{inbox[0].ID}, work.ID); err != nil {
		t.Fatal(err)
	}
	code := connectPair(t, url, a, b, true)
	if e := serverEntries(t, url, code, docAccounts)["t/"+mb]; e.D || len(e.V) == 0 {
		t.Fatal("the sign-in didn't sync")
	}
	if err := a.RemoveAccount("a-gmail"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b, a)
	if _, accts := b.mailboxes(); len(accts) != 0 {
		t.Fatal("the mailbox is still on the other computer")
	}
	check := func(doc string, mine func(string) bool) {
		t.Helper()
		for k, e := range serverEntries(t, url, code, doc) {
			if mine(k) && !e.D {
				t.Fatalf("%s: %s is still on the server", doc, k)
			}
		}
	}
	scoped := func(k string) bool { _, m, _ := splitKey(k); return m == mb }
	check(docAccounts, scoped)
	check(docSettings, scoped)
	check(docLabels, scoped)
	check(marksDoc(mb), func(string) bool { return true })
	if len(serverEntries(t, url, code, marksDoc(mb))) == 0 {
		t.Fatal("the label decisions never synced")
	}

	// Added again on b: a fresh mailbox, without the old choices.
	addDemoAccount(t, b, 0, "b-gmail")
	syncDevices(t, b, a, b)
	if _, err := a.store.Account("a-gmail"); err == nil {
		t.Fatal("ids are per computer")
	}
	local, _ := a.mailboxes()
	again, ok := local[mb]
	if !ok {
		t.Fatal("the mailbox added again didn't sync")
	}
	if as := a.Settings(); slices.Contains(as.MutedMailboxes, again.ID) || len(as.NotJunkSenders[again.ID]) > 0 {
		t.Fatalf("old choices came back: %+v %+v", as.MutedMailboxes, as.NotJunkSenders)
	}
	if tok, err := auth.LoadToken(a.secrets, again.ID); err != nil || tok.RefreshToken != "rt-b-gmail" {
		t.Fatalf("sign-in %+v %v", tok, err)
	}
}

// One mailbox changing on a computer doesn't reorder the others'.
func TestDeviceSyncOrderStaysWhenOneMailboxChanges(t *testing.T) {
	url, _ := newSyncServer(t)
	a, b := newBlankService(t), newBlankService(t)
	addDemoAccount(t, a, 0, "a-gmail")
	addDemoAccount(t, a, 1, "a-outlook")
	connectPair(t, url, a, b, false)
	order := func(s *Service) []string {
		_, l := s.mailboxes()
		var out []string
		for _, x := range l {
			out = append(out, x.Email)
		}
		return out
	}
	before := order(b)
	if !slices.Equal(before, order(a)) {
		t.Fatalf("order %v vs %v", before, order(a))
	}
	if err := a.UpdateAccountSignature("a-outlook", "<p>sig</p>"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b)
	if after := order(b); !slices.Equal(before, after) {
		t.Fatalf("order changed: %v -> %v", before, after)
	}
}

// A label decision whose label hasn't synced yet waits for it.
func TestDeviceSyncMarkWaitsForItsLabel(t *testing.T) {
	s := newBlankService(t)
	acct := addDemoAccount(t, s, 0, "m-gmail")
	doc := marksDoc(mailboxKey(acct))
	none := func(string) (json.RawMessage, bool) { return nil, false }
	notApplied, err := syncSource{s}.Apply(doc, map[string]json.RawMessage{"msg-1": json.RawMessage(`[{"l":"not-yet-synced","k":"manual"}]`)}, none)
	if err != nil || !slices.Equal(notApplied, []string{"msg-1"}) {
		t.Fatalf("not applied %v %v", notApplied, err)
	}
}

// A mailbox added on one computer after both synced keeps its choices on
// the other: the defaults it starts with there don't count as changes.
func TestDeviceSyncNewMailboxKeepsItsChoices(t *testing.T) {
	url, _ := newSyncServer(t)
	a, b := newBlankService(t), newBlankService(t)
	addDemoAccount(t, a, 0, "a-gmail")
	code := connectPair(t, url, a, b, true)
	addDemoAccount(t, a, 1, "a-outlook")
	st := a.GetSettings()
	st.MutedMailboxes = append(st.MutedMailboxes, "a-outlook")
	st.NotJunkSenders = map[string][]string{"a-outlook": {"friend@example.com"}}
	if err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b, a, b)
	local, _ := b.mailboxes()
	var outlook models.Account
	for _, x := range local {
		if x.Provider == models.ProviderOutlook {
			outlook = x
		}
	}
	bs := b.Settings()
	if outlook.ID == "" || !slices.Contains(bs.MutedMailboxes, outlook.ID) || !slices.Equal(bs.NotJunkSenders[outlook.ID], []string{"friend@example.com"}) {
		t.Fatalf("b: %+v muted %v notJunk %v", outlook.ID, bs.MutedMailboxes, bs.NotJunkSenders)
	}
	if as := a.Settings(); !slices.Contains(as.MutedMailboxes, "a-outlook") {
		t.Fatal("a lost its choice")
	}

	// And then nothing more to upload, round after round.
	revs := serverRevs(t, url, code)
	syncDevices(t, a, b, a, b, a, b)
	if after := serverRevs(t, url, code); !reflect.DeepEqual(revs, after) {
		t.Fatalf("converged computers uploaded: %v -> %v", revs, after)
	}
}

// A computer whose sign-in stopped working doesn't remove the shared one.
func TestDeviceSyncBrokenSignInKeepsTheSharedOne(t *testing.T) {
	url, _ := newSyncServer(t)
	a, b := newBlankService(t), newBlankService(t)
	acct := addDemoAccount(t, a, 0, "a-gmail")
	code := connectPair(t, url, a, b, true)
	key := "t/" + mailboxKey(acct)
	if err := a.store.SetNeedsReauth("a-gmail", true); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, a, b, a)
	if e := serverEntries(t, url, code, docAccounts)[key]; e.D || len(e.V) == 0 {
		t.Fatal("the shared sign-in was removed")
	}
}

// Settings another computer changed are applied without losing a save made
// here at the same moment.
func TestDeviceSyncApplyDoesNotLoseASave(t *testing.T) {
	s := newBlankService(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 100 {
			theme := []string{"dark", "light"}[i%2]
			if _, err := s.applySettings(map[string]json.RawMessage{"g/theme": rawJSON(theme)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 100 {
			if err := s.editSettings(func(st *models.Settings) error {
				st.TrustedLinkDomains = append(slices.Clone(st.TrustedLinkDomains), fmt.Sprintf("d%d.example.com", i))
				return nil
			}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	if n := len(s.Settings().TrustedLinkDomains); n != 100 {
		t.Fatalf("%d of 100 saves kept", n)
	}
}
