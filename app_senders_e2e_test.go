package main

import (
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
)

// "Clean up by sender": the inbox's senders, biggest first, per address or
// per company domain (shared mail services and the mailbox's own domain
// stay per address); "Archive all" archives exactly that sender's inbox
// mail, and junking a sender takes it off the list.
func TestTopSendersCleanUp(t *testing.T) {
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
	deliver := func(from, name string, ago time.Duration, read bool) string {
		return e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "From " + from, FromName: name, From: from, Received: now.Add(-ago), IsRead: read})
	}
	var promo []string
	for i := range 4 {
		promo = append(promo, deliver("promo@deals.example", "Deals", time.Duration(i+1)*time.Hour, i == 0))
	}
	look := deliver("breaking.promo@deals.example", "Deals Alerts", 10*time.Hour, false)
	deliver("news@deals.example", "Deals News", 11*time.Hour, false)
	deliver("someone@gmail.com", "Someone", 12*time.Hour, false)
	deliver("other@gmail.com", "Other", 13*time.Hour, false)
	deliver("colleague@contoso.example", "Colleague", 14*time.Hour, false)
	deliver("bob@contoso.example", "Bob Builder", 15*time.Hour, false) // sent to myself
	e.graph.Deliver(fakes.GraphMessage{Folder: "archive", Subject: "Old deal", From: "promo@deals.example", Received: now.Add(-20 * time.Hour)})
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}

	top, err := m.TopSenders(oa.ID, "", "address", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 6 {
		t.Fatalf("senders %+v", top)
	}
	first := top[0]
	if first.Key != "promo@deals.example" || first.Count != 4 || first.Unread != 3 || first.Name != "Deals" || first.AccountID != oa.ID ||
		first.Domain != "deals.example" || first.Addresses != 1 || !first.Newest.Equal(now.Add(-time.Hour)) {
		t.Fatalf("top sender %+v", first)
	}
	for _, s := range top {
		if s.Address == "bob@contoso.example" {
			t.Fatal("own mail listed")
		}
	}

	byDomain, err := m.TopSenders(oa.ID, "inbox", "domain", 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]models.SenderSummary{}
	for _, s := range byDomain {
		keys[s.Key] = s
	}
	if d := keys["@deals.example"]; byDomain[0].Key != "@deals.example" || d.Count != 6 || d.Addresses != 3 || d.Address != "promo@deals.example" {
		t.Fatalf("domain group %+v", byDomain)
	}
	if _, ok := keys["@gmail.com"]; ok || keys["someone@gmail.com"].Count != 1 || keys["colleague@contoso.example"].Count != 1 || len(byDomain) != 4 {
		t.Fatalf("shared and own domains must stay per address: %+v", byDomain)
	}
	if limited, _ := m.TopSenders(oa.ID, "", "address", 2); len(limited) != 2 {
		t.Fatalf("limit: %+v", limited)
	}

	// Archive all: exactly that address's inbox mail (not the look-alike).
	ids, err := m.SenderMessageIDs(oa.ID, "", "promo@deals.example")
	if err != nil || len(ids) != 4 {
		t.Fatalf("sender ids %v %v", ids, err)
	}
	if err := m.Archive(ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range promo {
		id := id
		eventually(t, "sender mail archived", func() bool { return e.graph.FolderOf(id) == "archive" })
	}
	if e.graph.FolderOf(look) != "inbox" {
		t.Fatal("look-alike sender archived")
	}
	top, _ = m.TopSenders(oa.ID, "", "address", 0)
	if top[0].Key == "promo@deals.example" || len(top) != 5 {
		t.Fatalf("after archive %+v", top)
	}

	// Junking the domain takes it off the list.
	if _, err := m.JunkSender(oa.ID, "news@deals.example", "domain", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "domain junked", func() bool {
		after, _ := m.TopSenders(oa.ID, "", "domain", 0)
		for _, s := range after {
			if s.Domain == "deals.example" {
				return false
			}
		}
		return len(after) == 3
	})
}
