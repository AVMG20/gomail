package main

import (
	"slices"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
)

// "Always send <sender> to Junk" adds a junk rule for that mailbox, moves
// the sender's mail already in the inbox to Junk marked read, and junks
// (and marks read) the sender's new mail. "Not junk" brings mail back and
// reports the rule, which "Stop junking" removes.
func TestJunkSenderMovesExistingAndNewMail(t *testing.T) {
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
	picked := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Deal one", FromName: "Promo", From: "promo@deals.example", Received: now.Add(-time.Hour)})
	older := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Deal two", FromName: "Promo", From: "promo@deals.example", Received: now.Add(-2 * time.Hour)})
	sibling := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Other deal", From: "news@deals.example", Received: now.Add(-3 * time.Hour)})
	ham := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Lunch tomorrow", From: "eve@contoso.example", Received: now.Add(-4 * time.Hour)})
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}

	// The list sends conversation rows by conversation id.
	page, err := m.ListMessages(models.ListQuery{AccountID: oa.ID, FolderKind: models.FolderInbox, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	rowID := ""
	for _, r := range page.Messages {
		if r.Subject == "Deal one" {
			rowID = r.ID
			if r.ConversationID != "" {
				rowID = r.ConversationID
			}
		}
	}
	res, err := m.JunkSender(oa.ID, "Promo@Deals.example", "address", []string{rowID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Rule.Contains != "promo@deals.example" || res.Rule.AccountID != oa.ID || res.Count != 2 || len(res.Moved) != 1 || len(res.Unread) != 2 {
		t.Fatalf("junk sender result %+v", res)
	}
	for _, id := range []string{picked, older} {
		id := id
		eventually(t, "sender mail in Junk and read", func() bool {
			gm, _ := e.graph.Message(id)
			return e.graph.FolderOf(id) == "junkemail" && gm.IsRead
		})
	}
	for _, id := range []string{sibling, ham} {
		if gm, _ := e.graph.Message(id); e.graph.FolderOf(id) != "inbox" || gm.IsRead {
			t.Errorf("other mail touched: %s in %s", gm.Subject, e.graph.FolderOf(id))
		}
	}

	// Again: no duplicate rule.
	again, err := m.JunkSender(oa.ID, "promo@deals.example", "address", nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ = m.GetSettings()
	if again.Created || again.Rule.ID != res.Rule.ID || len(st.LabelRules) != 1 {
		t.Fatalf("second junk sender %+v, rules %+v", again, st.LabelRules)
	}
	if r := st.LabelRules[0]; r.Action != "junk" || r.AccountID != oa.ID || r.Field != "from" {
		t.Fatalf("rule %+v", r)
	}

	// New mail from the sender goes to Junk, read.
	fresh := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Deal three", From: "promo@deals.example", Received: now})
	if err := m.SyncNow(oa.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "new sender mail in Junk and read", func() bool {
		gm, _ := e.graph.Message(fresh)
		return e.graph.FolderOf(fresh) == "junkemail" && gm.IsRead
	})

	// Not junk: back to the inbox, and the rule is reported.
	nj, err := m.NotJunk([]string{e.msg(oa.ID, "Deal two").ID})
	if err != nil {
		t.Fatal(err)
	}
	if nj.Count != 1 || len(nj.Rules) != 1 || nj.Rules[0].ID != res.Rule.ID {
		t.Fatalf("not junk result %+v", nj)
	}
	eventually(t, "not junk back in the inbox", func() bool { return e.graph.FolderOf(older) == "inbox" })
	if err := m.RemoveRules([]string{res.Rule.ID}); err != nil {
		t.Fatal(err)
	}
	if st, _ = m.GetSettings(); len(st.LabelRules) != 0 {
		t.Fatalf("rule not removed: %+v", st.LabelRules)
	}
}

// The domain scope junks every address of the domain, but never a shared
// mail service's.
func TestJunkSenderDomain(t *testing.T) {
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
	a := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "One", From: "promo@deals.example", Received: now})
	b := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Two", From: "news@deals.example", Received: now.Add(-time.Hour)})
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.JunkSender(oa.ID, "someone@gmail.com", "domain", nil); err == nil {
		t.Fatal("junked a free mail domain")
	}
	if _, err := m.JunkSender(oa.ID, "colleague@contoso.example", "domain", nil); err == nil {
		t.Fatal("junked the mailbox's own domain")
	}
	res, err := m.JunkSender(oa.ID, "promo@deals.example", "domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rule.Contains != "@deals.example" || res.Count != 2 {
		t.Fatalf("domain result %+v", res)
	}
	for _, id := range []string{a, b} {
		id := id
		eventually(t, "domain mail in Junk", func() bool { return e.graph.FolderOf(id) == "junkemail" })
	}
	// The domain rule covers its addresses: no extra rule.
	if again, err := m.JunkSender(oa.ID, "other@deals.example", "address", nil); err != nil || again.Created {
		t.Fatalf("address under a junked domain: %+v %v", again, err)
	}
}

// "Always label <sender> as …" adds a label rule for the mailbox and labels
// the sender's mail already there; asking again adds no second rule.
func TestLabelSender(t *testing.T) {
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
	e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Weekly digest", From: "digest@news.example", Received: now})
	e.graph.Deliver(fakes.GraphMessage{Folder: "archive", Subject: "Older digest", From: "digest@news.example", Received: now.Add(-time.Hour)})
	e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Hello", From: "eve@contoso.example", Received: now.Add(-2 * time.Hour)})
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	st, _ = m.GetSettings()
	st.Labels = append(st.Labels, models.Label{Name: "Newsletters", Color: "#3b82f6", Source: "user", AccountIDs: []string{oa.ID}})
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	st, _ = m.GetSettings()
	news := ""
	for _, l := range st.Labels {
		if l.Name == "Newsletters" {
			news = l.ID
		}
	}
	res, err := m.LabelSender(oa.ID, "digest@news.example", "address", news)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Rule.LabelID != news || res.Rule.AccountID != oa.ID || len(res.Labeled) != 2 {
		t.Fatalf("label sender result %+v", res)
	}
	for _, subj := range []string{"Weekly digest", "Older digest"} {
		if !slices.Contains(e.msg(oa.ID, subj).Labels, news) {
			t.Errorf("%q not labeled", subj)
		}
	}
	if slices.Contains(e.msg(oa.ID, "Hello").Labels, news) {
		t.Error("other sender labeled")
	}
	again, err := m.LabelSender(oa.ID, "digest@news.example", "address", news)
	if err != nil || again.Created || again.Rule.ID != res.Rule.ID {
		t.Fatalf("second label sender %+v %v", again, err)
	}
	if err := m.RemoveRules([]string{res.Rule.ID}); err != nil {
		t.Fatal(err)
	}
	if st, _ = m.GetSettings(); len(st.LabelRules) != 0 {
		t.Fatalf("rule not removed: %+v", st.LabelRules)
	}
}
