package gmail

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/provider"
)

// A history record that only changed labels is applied from the record for
// messages synced before; new mail, messages not synced here and records
// without labels are fetched. A label made since the labels were loaded is
// picked up.
func TestHistoryLabelChangesSkipFetch(t *testing.T) {
	var mu sync.Mutex
	fetched := map[string]int{}
	labelLists := 0
	c := auditServer(t, func(w http.ResponseWriter, r *http.Request, p string) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case p == "labels":
			labelLists++
			labels := []map[string]string{{"id": "INBOX", "type": "system"}, {"id": "UNREAD", "type": "system"},
				{"id": "STARRED", "type": "system"}, {"id": "Label_1", "name": "Work", "type": "user"}}
			if labelLists > 1 {
				labels = append(labels, map[string]string{"id": "Label_9", "name": "Later", "type": "user"})
			}
			writeJSON(w, map[string]any{"labels": labels})
		case p == "profile":
			writeJSON(w, map[string]any{"historyId": "100"})
		case p == "messages" && r.URL.Query().Get("labelIds") == "INBOX":
			writeJSON(w, map[string]any{"messages": []map[string]string{{"id": "m1"}, {"id": "m2"}, {"id": "m5"}}})
		case p == "messages":
			writeJSON(w, map[string]any{})
		case strings.HasPrefix(p, "messages/"):
			id := strings.TrimPrefix(p, "messages/")
			fetched[id]++
			writeJSON(w, map[string]any{"id": id, "threadId": "t" + id, "labelIds": []string{"INBOX", "UNREAD", "Label_1"}, "snippet": "about " + id,
				"internalDate": "1790000000000", "payload": map[string]any{"mimeType": "text/plain",
					"headers": []map[string]string{{"name": "Subject", "value": "Subject " + id}, {"name": "From", "value": "ann@example.com"}}}})
		case p == "history":
			change := func(id string, labels ...string) map[string]any {
				m := map[string]any{"id": id, "threadId": "t" + id}
				if labels != nil {
					m["labelIds"] = labels
				}
				return map[string]any{"message": m}
			}
			writeJSON(w, map[string]any{"historyId": "150", "history": []map[string]any{
				{"labelsAdded": []any{change("m2", "INBOX", "UNREAD", "Label_1", "STARRED")}},
				{"labelsRemoved": []any{change("m1", "INBOX", "Label_1")}},          // read
				{"labelsAdded": []any{change("m2", "INBOX", "STARRED", "Label_9")}}, // latest wins
				{"labelsAdded": []any{change("m3", "INBOX")}},                       // not synced here
				{"messagesAdded": []any{change("m4", "INBOX", "UNREAD")}},           // new mail
				{"labelsRemoved": []any{change("m5")}},                              // no labels in the record
			}})
		default:
			return false
		}
		return true
	})
	ctx := context.Background()
	if _, err := c.Sync(ctx, provider.SyncRequest{}); err != nil {
		t.Fatal(err)
	}
	res, err := c.Sync(ctx, provider.SyncRequest{State: map[string]string{"historyId": "100"}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if fetched["m1"] != 1 || fetched["m2"] != 1 || fetched["m3"] != 1 || fetched["m4"] != 1 || fetched["m5"] != 2 {
		t.Fatalf("fetches %v", fetched)
	}
	if labelLists != 2 {
		t.Fatalf("labels listed %d times (want a reload for the new label)", labelLists)
	}
	byID := map[string]mail.Message{}
	for _, m := range res.Upserts {
		byID[m.RemoteID] = m
	}
	if len(byID) != 5 {
		t.Fatalf("upserts %v", res.Upserts)
	}
	m1, m2 := byID["m1"], byID["m2"]
	if m1.Unread || m1.Subject != "Subject m1" || m1.Snippet != "about m1" || m1.From.Email != "ann@example.com" ||
		strings.Join(m1.Labels, ",") != "Work" || strings.Join(m1.FolderRemoteIDs, ",") != "INBOX,ALL_MAIL" {
		t.Fatalf("m1 %+v", m1)
	}
	if m2.Unread || !m2.Starred || !slices.Equal(m2.Labels, []string{"Later"}) || m2.Subject != "Subject m2" {
		t.Fatalf("m2 %+v", m2)
	}
}
