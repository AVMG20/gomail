package main

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
)

// A label's "When added" moves new mail a rule or Jev labels: Archive keeps
// it unread, Trash from a rule trashes it read, and Jev's Trash picks land in
// Junk instead (a wrong guess must not delete mail).
func TestLabelActionsMoveNewMail(t *testing.T) {
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
	st, _ = m.GetSettings()
	ids := map[string]string{}
	for i, l := range st.Labels {
		if len(l.AccountIDs) == 1 && l.AccountIDs[0] == oa.ID {
			switch l.Name {
			case "Newsletters":
				st.Labels[i].Then = "archive"
			case "Promotions":
				st.Labels[i].Then = "trash"
			}
			ids[l.Name] = l.ID
		}
	}
	if ids["Newsletters"] == "" || ids["Promotions"] == "" {
		t.Fatalf("mailbox labels %v", ids)
	}
	st.LabelRules = append(st.LabelRules,
		models.LabelRule{ID: "r1", AccountID: oa.ID, Field: "from", Contains: "digest@news.example", Action: "label", LabelID: ids["Newsletters"]},
		models.LabelRule{ID: "r2", AccountID: oa.ID, Field: "from", Contains: "deals@shop.example", Action: "label", LabelID: ids["Promotions"]})
	st.AutoLabelAccounts = []string{oa.ID}
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	e.ai.Score = func(q string, state map[string]any) float64 {
		if q == "label_"+ids["Promotions"] && strings.Contains(state["subject"].(string), "Flash sale") {
			return 0.95
		}
		return 0.02
	}
	if err := m.SetOpenRouterKey("sk-or-e2e"); err != nil {
		t.Fatal(err)
	}
	digest := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Weekly digest", From: "digest@news.example", Received: now})
	deal := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Coupons", From: "deals@shop.example", Received: now})
	sale := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Flash sale today", From: "store@retail.example", Received: now})
	plain := e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Lunch", From: "eve@contoso.example", Received: now})
	if err := m.SyncNow(oa.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "label actions applied", func() bool {
		return e.graph.FolderOf(digest) == "archive" && e.graph.FolderOf(deal) == "deleteditems" && e.graph.FolderOf(sale) == "junkemail"
	})
	if gm, _ := e.graph.Message(digest); gm.IsRead {
		t.Error("archived newsletter was marked read")
	}
	for _, id := range []string{deal, sale} {
		if gm, _ := e.graph.Message(id); !gm.IsRead {
			t.Errorf("%s moved but still unread", id)
		}
	}
	if e.graph.FolderOf(plain) != "inbox" {
		t.Errorf("plain mail moved to %s", e.graph.FolderOf(plain))
	}
	// The review lists what was sorted where, and why.
	reasons := map[string]string{}
	for _, kind := range []string{models.FolderArchive, models.FolderSpam, models.FolderTrash} {
		moves, err := m.AutoMoves(oa.ID, kind, 24)
		if err != nil {
			t.Fatal(err)
		}
		for _, mv := range moves {
			reasons[mv.Subject] = mv.To + ": " + mv.Reason
		}
	}
	want := map[string]string{
		"Weekly digest":    "archive: Your rule labeled it “Newsletters”",
		"Coupons":          "trash: Your rule labeled it “Promotions”",
		"Flash sale today": "spam: Jev labeled it “Promotions”",
	}
	for subj, w := range want {
		if reasons[subj] != w {
			t.Errorf("review for %q = %q, want %q", subj, reasons[subj], w)
		}
	}
	if len(reasons) != len(want) {
		t.Errorf("review lists %v", reasons)
	}
}
