package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/devsync"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncapi"
	"github.com/AVMG20/gomail/internal/syncserver"
)

// Every settings field is classified: synced, kept per computer, or synced
// elsewhere. A new field fails here until it is put in one of the lists.
func TestDeviceSyncClassifiesEverySetting(t *testing.T) {
	seen := map[string]string{}
	for list, names := range map[string][]string{"synced": syncedSettings, "device": deviceSettings, "other": otherSettings} {
		for _, n := range names {
			if prev, dup := seen[n]; dup {
				t.Errorf("%s is in %s and %s", n, prev, list)
			}
			seen[n] = list
		}
	}
	typ := reflect.TypeFor[models.Settings]()
	fields := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		fields[name] = true
		if _, ok := seen[name]; !ok {
			t.Errorf("setting %q isn't classified for device sync", name)
		}
	}
	for n := range seen {
		if !fields[n] {
			t.Errorf("%q isn't a setting", n)
		}
	}
}

func newSyncServer(t *testing.T) (url, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	srv, err := syncserver.New(syncserver.Config{DataDir: dataDir, Signups: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return ts.URL, dataDir
}

// addDemoAccount adds a demo mailbox under its own id, signed in, and
// downloads its mail.
func addDemoAccount(t *testing.T, s *Service, i int, id string) models.Account {
	t.Helper()
	a := demo.Accounts()[i]
	a.ID, a.CreatedAt = id, time.Now()
	if err := s.store.InsertAccount(a); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveToken(s.secrets, id, &oauth2.Token{AccessToken: "at-" + id, RefreshToken: "rt-" + id}); err != nil {
		t.Fatal(err)
	}
	s.labelsGainAccount(id)
	if err := s.SyncNow(id); err != nil {
		t.Fatal(err)
	}
	return a
}

func syncDevices(t *testing.T, ss ...*Service) {
	t.Helper()
	for _, s := range ss {
		if st, err := s.SyncDevicesNow(); err != nil {
			t.Fatalf("sync: %v (%+v)", err, st)
		}
	}
}

func labelNamed(t *testing.T, s *Service, accountID, name string) models.Label {
	t.Helper()
	l, ok := config.LabelNamed(s.Settings().Labels, accountID, name)
	if !ok {
		t.Fatalf("no label %q in %s: %+v", name, accountID, s.Settings().Labels)
	}
	return l
}

func firstInbox(t *testing.T, s *Service, accountID string) []models.MessageSummary {
	t.Helper()
	page, err := s.ListMessages(models.ListQuery{AccountID: accountID, FolderKind: models.FolderInbox, Limit: 100})
	if err != nil || len(page.Messages) < 3 {
		t.Fatalf("inbox %d %v", len(page.Messages), err)
	}
	return page.Messages
}

func byRemote(t *testing.T, s *Service, accountID, remoteID string) models.MessageSummary {
	t.Helper()
	ids, err := s.ResolveQueryIDs(models.ListQuery{AccountID: accountID}, 10000)
	if err != nil {
		t.Fatal(err)
	}
	sums, _ := s.store.Summaries(ids)
	for _, m := range sums {
		if m.RemoteID == remoteID {
			return m
		}
	}
	t.Fatalf("no message %s in %s", remoteID, accountID)
	return models.MessageSummary{}
}

func hasLabel(m models.MessageSummary, id, source string) bool {
	return slices.ContainsFunc(m.LabelDetails, func(d models.MessageLabel) bool { return d.LabelID == id && (source == "" || d.Source == source) })
}

func TestDeviceSyncTwoComputers(t *testing.T) {
	url, serverDir := newSyncServer(t)

	// ---- the work laptop: two mailboxes, customized ----
	work := newBlankService(t)
	addDemoAccount(t, work, 0, "w-gmail")
	addDemoAccount(t, work, 1, "w-outlook")
	st := work.GetSettings()
	st.Theme, st.PrimaryColor, st.Zoom, st.DeviceSyncInterval = "dark", "#10b981", 1.5, 15
	st.GoogleClientID, st.GoogleClientSecret = "client-id.apps.example.com", "client-secret-value"
	st.Labels = append(st.Labels, models.Label{Name: "Side Project", Color: "#123456", Description: "Mail about the side project.", AccountIDs: []string{"w-gmail"}, Then: "archive"})
	st.MutedMailboxes = []string{"w-outlook"}
	st.NotJunkSenders = map[string][]string{"w-gmail": {"friend@example.com"}}
	if err := work.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	side := labelNamed(t, work, "w-gmail", "Side Project")
	st = work.GetSettings()
	st.LabelRules = append(st.LabelRules, models.LabelRule{AccountID: "w-gmail", Field: "from", Contains: "boss@example.com", Action: "label", LabelID: side.ID})
	st.QuietLabels = map[string][]string{"w-gmail": {side.ID}}
	if err := work.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := work.SetOpenRouterKey("sk-or-test-key-123"); err != nil {
		t.Fatal(err)
	}
	if err := work.UpdateAccountSignature("w-gmail", "<p>Best, Alex</p>"); err != nil {
		t.Fatal(err)
	}
	inbox := firstInbox(t, work, "w-gmail")
	manual, rejected := inbox[0], inbox[1]
	if _, err := work.AddLabel([]string{manual.ID}, side.ID); err != nil {
		t.Fatal(err)
	}
	finance := labelNamed(t, work, "w-gmail", "Finance")
	if _, _, err := work.store.ApplyAIScores(rejected.ID, map[string]float64{finance.ID: 0.9}, store.AIPolicy{AddThreshold: 0.5}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := work.RemoveLabel([]string{rejected.ID}, finance.ID); err != nil {
		t.Fatal(err)
	}
	if err := work.store.ReplaceCalendars("w-gmail", []calendar.Calendar{{RemoteID: "cal-team", Name: "Team"}}); err != nil {
		t.Fatal(err)
	}
	if err := work.store.SetCalendarVisible(store.CalendarID("w-gmail", "cal-team"), false); err != nil {
		t.Fatal(err)
	}

	if _, err := work.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Create: true, IncludeSecrets: true}); err != nil {
		t.Fatal(err)
	}
	code, err := work.DeviceSyncCode()
	if err != nil || len(strings.Fields(code)) != 24 {
		t.Fatalf("sync code %q %v", code, err)
	}
	syncDevices(t, work)

	// ---- home: added the Outlook mailbox itself earlier (another local id) ----
	home := newBlankService(t)
	addDemoAccount(t, home, 1, "h-outlook")
	hs := home.GetSettings()
	hs.Theme, hs.Zoom = "light", 0.8
	if err := home.SaveSettings(hs); err != nil {
		t.Fatal(err)
	}
	if _, err := home.ConnectDeviceSync(models.DeviceSyncConnect{ServerURL: url, Code: strings.ToUpper(code)}); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, home)

	accts, _ := home.Accounts()
	if len(accts) != 2 {
		t.Fatalf("home mailboxes %+v", accts)
	}
	var hGmail models.Account
	for _, a := range accts {
		if a.Provider == models.ProviderGmail {
			hGmail = a
		}
	}
	// The Gmail mailbox arrived signed in (its sign-in synced) and in order.
	if hGmail.ID == "" || hGmail.NeedsReauth || hGmail.Signature != "<p>Best, Alex</p>" || hGmail.Color != "#ea4335" || accts[0].ID != hGmail.ID {
		t.Fatalf("synced mailbox %+v (order %s, %s)", hGmail, accts[0].ID, accts[1].ID)
	}
	if tok, err := auth.LoadToken(home.secrets, hGmail.ID); err != nil || tok.RefreshToken != "rt-w-gmail" {
		t.Fatalf("synced sign-in %+v %v", tok, err)
	}
	if tok, _ := auth.LoadToken(home.secrets, "h-outlook"); tok.RefreshToken != "rt-h-outlook" {
		t.Fatal("a working sign-in was replaced")
	}
	hs = home.GetSettings()
	if hs.Theme != "dark" || hs.PrimaryColor != "#10b981" || hs.Zoom != 0.8 || hs.DeviceSyncInterval != 1 {
		t.Fatalf("settings: theme %s color %s zoom %v", hs.Theme, hs.PrimaryColor, hs.Zoom)
	}
	if hs.GoogleClientID != "client-id.apps.example.com" || hs.GoogleClientSecret != "client-secret-value" || !hs.OpenRouterKeySet {
		t.Fatalf("secrets didn't sync: %+v", hs)
	}
	if !slices.Contains(hs.MutedMailboxes, "h-outlook") || !slices.Equal(hs.NotJunkSenders[hGmail.ID], []string{"friend@example.com"}) {
		t.Fatalf("per-mailbox settings: muted %v notJunk %v", hs.MutedMailboxes, hs.NotJunkSenders)
	}
	hSide := labelNamed(t, home, hGmail.ID, "Side Project")
	if hSide.Color != "#123456" || hSide.Then != "archive" || hSide.Description == "" {
		t.Fatalf("label %+v", hSide)
	}
	if !slices.ContainsFunc(hs.LabelRules, func(r models.LabelRule) bool {
		return r.AccountID == hGmail.ID && r.Contains == "boss@example.com" && r.LabelID == hSide.ID
	}) || !slices.Equal(hs.QuietLabels[hGmail.ID], []string{hSide.ID}) {
		t.Fatalf("rules %+v quiet %v", hs.LabelRules, hs.QuietLabels)
	}
	if cals, _ := home.store.ExportCalendarPrefs(hGmail.ID); len(cals) != 1 || cals[0].RemoteID != "cal-team" || cals[0].Visible {
		t.Fatalf("calendar choices %+v", cals)
	}

	// Label decisions apply as the mail downloads.
	if err := home.SyncNow(hGmail.ID); err != nil {
		t.Fatal(err)
	}
	if m := byRemote(t, home, hGmail.ID, manual.RemoteID); !hasLabel(m, hSide.ID, "manual") {
		t.Fatalf("manual label didn't sync: %+v", m.LabelDetails)
	}
	hFinance := labelNamed(t, home, hGmail.ID, "Finance")
	if m := byRemote(t, home, hGmail.ID, rejected.RemoteID); hasLabel(m, hFinance.ID, "") {
		t.Fatalf("removed label came back: %+v", m.LabelDetails)
	}

	// ---- home changes things; work follows ----
	syncDevices(t, work) // home's Outlook labels and anything else it added
	time.Sleep(5 * time.Millisecond)
	hs = home.GetSettings()
	hs.Theme = "light"
	for i, l := range hs.Labels {
		if l.ID == hSide.ID {
			hs.Labels[i].Name, hs.Labels[i].Color = "Hobby", "#654321"
		}
	}
	if err := home.SaveSettings(hs); err != nil {
		t.Fatal(err)
	}
	if err := home.RemoveLabel([]string{byRemote(t, home, hGmail.ID, manual.RemoteID).ID}, hSide.ID); err != nil {
		t.Fatal(err)
	}
	if err := home.UpdateAccount(hGmail.ID, "Personal Gmail", "#7c3aed"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, home, work)
	ws := work.GetSettings()
	if ws.Theme != "light" || ws.Zoom != 1.5 {
		t.Fatalf("work settings: theme %s zoom %v", ws.Theme, ws.Zoom)
	}
	if l := labelNamed(t, work, "w-gmail", "Hobby"); l.ID != side.ID || l.Color != "#654321" {
		t.Fatalf("renamed label %+v (was %s)", l, side.ID)
	}
	if m := byRemote(t, work, "w-gmail", manual.RemoteID); hasLabel(m, side.ID, "") {
		t.Fatalf("label removed at home is still on at work: %+v", m.LabelDetails)
	}
	if a, _ := work.store.Account("w-gmail"); a.DisplayName != "Personal Gmail" || a.Color != "#7c3aed" {
		t.Fatalf("account %+v", a)
	}

	// ---- quiet: nothing to do uploads nothing ----
	revs := serverRevs(t, url, code)
	syncDevices(t, work, home, work, home)
	if after := serverRevs(t, url, code); !reflect.DeepEqual(revs, after) {
		t.Fatalf("idle rounds uploaded: %v -> %v", revs, after)
	}

	// ---- a label deleted at work goes at home; a removed mailbox too ----
	ws = work.GetSettings()
	ws.Labels = slices.DeleteFunc(ws.Labels, func(l models.Label) bool { return l.ID == side.ID })
	if err := work.SaveSettings(ws); err != nil {
		t.Fatal(err)
	}
	if err := work.RemoveAccount("w-outlook"); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, work, home)
	if _, ok := config.LabelNamed(home.Settings().Labels, hGmail.ID, "Hobby"); ok {
		t.Fatal("deleted label still at home")
	}
	if _, err := home.store.Account("h-outlook"); err == nil {
		t.Fatal("removed mailbox still at home")
	}

	// ---- the server only has ciphertext ----
	db, err := os.ReadFile(filepath.Join(serverDir, "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	wal, _ := os.ReadFile(filepath.Join(serverDir, "sync.db-wal"))
	for _, plain := range []string{strings.Fields(code)[0] + " " + strings.Fields(code)[1], "Side Project", "sk-or-test-key-123", "client-secret-value", "rt-w-gmail", "alex.demo", "boss@example.com", "#10b981"} {
		if strings.Contains(string(db), plain) || strings.Contains(string(wal), plain) {
			t.Fatalf("server database contains %q", plain)
		}
	}

	// ---- turning secrets off removes them from the server ----
	if err := work.SetDeviceSyncSecrets(false); err != nil {
		t.Fatal(err)
	}
	syncDevices(t, work, home)
	if home.DeviceSyncStatus().IncludeSecrets {
		t.Fatal("the secrets switch didn't sync")
	}
	if !home.GetSettings().OpenRouterKeySet {
		t.Fatal("turning secret sync off removed a key from a computer")
	}

	// ---- disconnect keeps local data ----
	if err := home.DisconnectDeviceSync(); err != nil {
		t.Fatal(err)
	}
	if st := home.DeviceSyncStatus(); st.Connected || home.GetSettings().Theme != "light" {
		t.Fatalf("after disconnect %+v", st)
	}
	if raw, err := home.store.RawSetting(labelMapKey); err == nil {
		t.Fatalf("label map left behind: %s", raw)
	}
}

// serverRevs lists the revisions of the account's documents on the server.
func serverRevs(t *testing.T, url, code string) map[string]int64 {
	t.Helper()
	keys, _, _ := devsync.KeysFromCode(code)
	b, _ := json.Marshal(syncapi.LoginRequest{Account: keys.Account, AuthKey: base64.StdEncoding.EncodeToString(keys.Auth)})
	resp, err := http.Post(url+"/v1/login", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var login syncapi.LoginResponse
	err = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", url+"/v1/docs", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var l syncapi.DocList
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, d := range l.Docs {
		out[d.Name] = d.Rev
	}
	return out
}
