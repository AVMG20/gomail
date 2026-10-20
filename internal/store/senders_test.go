package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func senderMsg(i int, name, email string, unread bool, folders ...string) mail.Message {
	return mail.Message{
		RemoteID:        fmt.Sprintf("s%03d", i),
		FolderRemoteIDs: folders,
		From:            models.Address{Name: name, Email: email},
		Subject:         fmt.Sprintf("Mail %d", i),
		Date:            base.Add(time.Duration(i) * time.Hour),
		Unread:          unread,
	}
}

// Sender counts per address of a folder: case folded, newest name, unread,
// own mail and other folders left out, List-Unsubscribe of the newest.
func TestSenderStats(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	seedAccount(t, s, "a2")
	ups := []mail.Message{
		senderMsg(1, "Shop", "news@shop.example", true, "INBOX", "ALL"),
		senderMsg(2, "Shop News", "News@Shop.example", false, "INBOX", "ALL"),
		senderMsg(3, "Shop", "news@shop.example", true, "ALL"), // archived
		senderMsg(4, "Breaking", "breaking.news@shop.example", true, "INBOX", "ALL"),
		senderMsg(5, "Me", "a1@example.com", false, "INBOX", "ALL"), // sent to myself
		senderMsg(6, "Junk", "deals@spam.example", true, "SPAM"),
		senderMsg(7, "", "", true, "INBOX"),
	}
	ups[1].Unsubscribe = mail.Unsubscribe{URL: "https://shop.example/unsub"}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: ups}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySync("a2", mail.SyncResult{Upserts: []mail.Message{senderMsg(1, "Shop", "news@shop.example", false, "INBOX")}}, nil); err != nil {
		t.Fatal(err)
	}

	stats, err := s.SenderStats("a1", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	byAddr := map[string]SenderStat{}
	for _, st := range stats {
		byAddr[st.Address] = st
	}
	if len(stats) != 2 {
		t.Fatalf("stats %+v", stats)
	}
	news := byAddr["news@shop.example"]
	if news.Count != 2 || news.Unread != 1 || news.Name != "Shop News" || news.Newest != base.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("news %+v", news)
	}
	if b := byAddr["breaking.news@shop.example"]; b.Count != 1 || b.Unread != 1 {
		t.Fatalf("breaking %+v", b)
	}
	unsub, err := s.WithUnsubscribe([]int64{news.NewestRid, byAddr["breaking.news@shop.example"].NewestRid})
	if err != nil || !unsub[news.NewestRid] || len(unsub) != 1 {
		t.Fatalf("unsubscribe %v %v", unsub, err)
	}

	// Every mailbox: one row per mailbox and sender.
	all, err := s.SenderStats("", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all mailboxes %+v", all)
	}
	// "all": the archived message counts too; Junk never.
	recv, _ := s.SenderStats("a1", "all")
	for _, st := range recv {
		if st.Address == "news@shop.example" && st.Count != 3 {
			t.Fatalf("all kinds %+v", st)
		}
		if st.Address == "deals@spam.example" {
			t.Fatal("junk counted in all")
		}
	}
	if _, err := s.SenderStats("a1", "sent"); err == nil {
		t.Fatal("sent has no senders to clean up")
	}

	// One sender's mail: exact address (any case), or exactly the domain.
	ids, err := s.SenderMessageIDs("a1", "inbox", "NEWS@shop.example")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{MessageID("a1", "s002"), MessageID("a1", "s001")}
	if !slices.Equal(ids, want) {
		t.Fatalf("address ids %v, want %v", ids, want)
	}
	dom, _ := s.SenderMessageIDs("a1", "inbox", "@shop.example")
	if len(dom) != 3 {
		t.Fatalf("domain ids %v", dom)
	}
	if none, _ := s.SenderMessageIDs("a1", "inbox", "@example"); len(none) != 0 {
		t.Fatalf("partial domain matched %v", none)
	}
}

// The sender counts use the covering sender index and never sort.
func TestSenderStatsPlan(t *testing.T) {
	s := openTest(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT m.account_id, lower(m.from_email), m.from_name, count(*), sum(m.unread), max(m.date), m.rid
		FROM messages m WHERE m.account_id = ? AND (m.folder_kinds & ?) != 0 AND m.from_email != ''
		GROUP BY m.account_id, m.from_email COLLATE NOCASE`, "a1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "COVERING INDEX messages_senders") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("plan:\n%s", joined)
	}
}

// BenchmarkPerfTopSenders: sender counts of the perf fixture's mail.
//
//	go test ./internal/store -run '^$' -bench PerfTopSenders
func BenchmarkPerfTopSenders(b *testing.B) {
	f := perfDB(b)
	for _, tc := range []struct{ name, acct, kind string }{{"inbox", "g", "inbox"}, {"unified-inbox", "", "inbox"}, {"all-received", "g", "all"}} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.SenderStats(tc.acct, tc.kind); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
