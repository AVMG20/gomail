package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
)

// "Select all N in this view" resolves every id of the view (not just the
// loaded page) and the action reaches all of them, locally and on the server.
func TestSelectAllMarkReadCoversWholeView(t *testing.T) {
	e := newE2E(t)
	m := e.m
	now := time.Now().Truncate(time.Second)
	var ids []string
	for i := 0; i < 150; i++ {
		ids = append(ids, e.gmail.Deliver(fakes.GmailMessage{ThreadID: fmt.Sprintf("t%d", i), LabelIDs: []string{"INBOX", "UNREAD"},
			From: "News <news@example.com>", To: "alice@gmail.example", Subject: fmt.Sprintf("Mail %d", i), HTML: "<p>x</p>",
			Date: now.Add(-time.Duration(i) * time.Hour)}))
	}
	st, _ := m.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret = "google-client", "google-secret"
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	ga, err := m.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	q := models.ListQuery{AccountID: ga.ID, FolderKind: models.FolderInbox, Limit: 60}
	page, err := m.ListMessages(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("total %d, loaded %d", page.Total, len(page.Messages))
	all, err := m.ResolveQueryIDs(q, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != page.Total {
		t.Fatalf("resolved %d ids for a view of %d", len(all), page.Total)
	}
	// The frontend sends loaded rows by conversation id, the rest by message id.
	send := map[string]bool{}
	loaded := map[string]string{}
	for _, r := range page.Messages {
		loaded[r.ID] = r.ConversationID
	}
	for _, id := range all {
		if c := loaded[id]; c != "" {
			send[c] = true
		} else {
			send[id] = true
		}
	}
	var batch []string
	for id := range send {
		batch = append(batch, id)
	}
	if err := m.MarkRead(batch, true); err != nil {
		t.Fatal(err)
	}
	unreadLocal := 0
	page, _ = m.ListMessages(models.ListQuery{AccountID: ga.ID, FolderKind: models.FolderInbox, Limit: 500})
	for _, r := range page.Messages {
		if r.Unread {
			unreadLocal++
		}
	}
	unreadRemote := 0
	for _, id := range ids {
		g, _ := e.gmail.Message(id)
		for _, l := range g.LabelIDs {
			if l == "UNREAD" {
				unreadRemote++
			}
		}
	}
	if unreadLocal != 0 || unreadRemote != 0 {
		t.Fatalf("after select-all mark read: %d unread locally, %d on the server (of %d)", unreadLocal, unreadRemote, len(ids))
	}
}
