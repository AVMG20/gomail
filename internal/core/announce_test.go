package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// junkingOpenRouter is a Decisions fake that calls mail about a prize junk.
func junkingOpenRouter(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha/decisions":
			var req struct {
				Questions map[string]any `json:"questions"`
				State     map[string]any `json:"state"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			subject, _ := req.State["subject"].(string)
			answers := map[string]any{}
			for k := range req.Questions {
				p := 0.02
				if k == "label_junk" && strings.Contains(strings.ToLower(subject), "prize") {
					p = 0.97
				}
				answers[k] = map[string]any{"type": "noul", "noul": p}
			}
			json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]any{"cost": 0.00001}})
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"typesafe/jev-1.13","name":"Jev","pricing":{"prompt":"0.0000002","completion":"0"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeUI) newMail() []models.NewMailEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.NewMailEvent
	for i, ev := range f.events {
		if n, ok := f.payloads[i].(models.NewMailEvent); ok && ev == models.EventNewMail {
			out = append(out, n)
		}
	}
	return out
}

// With automatic labeling, new mail is announced after Jev labeled it:
// mail Jev moves to Junk never notifies, other new mail still does.
func TestNoNotificationForMailJevJunks(t *testing.T) {
	e := newEnv(t, junkingOpenRouter(t).URL)
	s := e.s
	if err := s.SetOpenRouterKey("sk-or-test"); err != nil {
		t.Fatal(err)
	}
	st := s.GetSettings()
	st.AutoLabelAccounts = []string{demo.GmailAccountID}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	acct := demo.GmailAccountID
	spam, err := s.store.FolderByKind(acct, models.FolderSpam)
	if err != nil {
		t.Fatal(err)
	}
	e.demos[acct].Deliver("You won a prize", "promo@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, _ := s.SearchMessages(models.SearchQuery{Query: "You won a prize", AccountID: acct})
		if len(res) > 0 && containsStr(res[0].Message.FolderIDs, spam.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Jev did not move the mail to Junk")
		}
		time.Sleep(20 * time.Millisecond)
	}

	e.demos[acct].Deliver("Lunch on Friday", "sam@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	for len(e.ui.newMail()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("mail Jev kept in the inbox was not announced")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	got := e.ui.newMail()
	if len(got) != 1 || got[0].Count != 1 || got[0].Latest.Subject != "Lunch on Friday" {
		t.Fatalf("new mail events %+v, want only the lunch mail", got)
	}
}
