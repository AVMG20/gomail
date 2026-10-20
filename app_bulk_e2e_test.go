package main

import (
	"fmt"
	"strings"
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

// Outlook's Flagged is no Graph folder: "Mark all as read" marks the unread
// flagged mail of every folder read on the server.
func TestMarkOutlookFlaggedRead(t *testing.T) {
	e := newE2E(t)
	m := e.m
	now := time.Now().Truncate(time.Second)
	f1 := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Flagged one", From: "a@contoso.example", Flagged: true, Received: now.Add(-time.Hour)})
	f2 := e.graph.Deliver(fakes.GraphMessage{Folder: "archive", Subject: "Flagged two", From: "b@contoso.example", Flagged: true, Received: now.Add(-2 * time.Hour)})
	plain := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Not flagged", From: "c@contoso.example", Received: now.Add(-3 * time.Hour)})
	st, _ := m.GetSettings()
	st.MicrosoftClientID = "ms-client"
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	oa, err := m.AddAccount(models.ProviderOutlook)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	folders, err := m.GetFolders(oa.ID)
	if err != nil {
		t.Fatal(err)
	}
	var flagged models.Folder
	for _, f := range folders {
		if f.Kind == models.FolderStarred {
			flagged = f
		}
	}
	if flagged.ID == "" || flagged.Unread != 2 {
		t.Fatalf("flagged folder %+v", flagged)
	}
	if err := m.MarkFolderRead(flagged.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f1, f2} {
		if gm, _ := e.graph.Message(id); !gm.IsRead {
			t.Errorf("flagged message %s still unread on the server", id)
		}
	}
	if gm, _ := e.graph.Message(plain); gm.IsRead {
		t.Error("unflagged mail was marked read")
	}
}

// New mail Jev labels Junk is marked read and moved to the Junk folder.
func TestAILabeledJunkMovesToJunk(t *testing.T) {
	e := newE2E(t)
	m := e.m
	now := time.Now().Truncate(time.Second)
	st, _ := m.GetSettings()
	st.MicrosoftClientID = "ms-client"
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	oa, err := m.AddAccount(models.ProviderOutlook)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	e.ai.Score = func(q string, state map[string]any) float64 {
		if q == "label_junk" && strings.Contains(state["subject"].(string), "casino") {
			return 0.97
		}
		return 0.02
	}
	if err := m.SetOpenRouterKey("sk-or-e2e"); err != nil {
		t.Fatal(err)
	}
	st, _ = m.GetSettings()
	st.AutoLabelAccounts = []string{oa.ID}
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	spam := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Win big at the casino", From: "promo@casino.example", Received: now})
	ham := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Lunch tomorrow", From: "eve@contoso.example", Received: now})
	if err := m.SyncNow(oa.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "AI junk moved to Junk and read", func() bool {
		gm, _ := e.graph.Message(spam)
		return e.graph.FolderOf(spam) == "junkemail" && gm.IsRead
	})
	if gm, _ := e.graph.Message(ham); e.graph.FolderOf(ham) != "inbox" || gm.IsRead {
		t.Errorf("normal mail touched: %+v", gm)
	}
}
