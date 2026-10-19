package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func seedSearch(t *testing.T) *Store {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := []mail.Message{
		msg(1, "Your invoice for October", "Acme Billing", "INBOX", "ALL"),
		msg(2, "Team offsite planning", "Dana Park", "INBOX", "ALL"),
		msg(3, "Re: Café menu <draft>", "José Álvarez", "INBOX", "ALL"),
		msg(4, "Weekly newsletter", "The Digest", "SPAM"),
		msg(5, "Payment received", "Acme Billing", "INBOX", "ALL"),
	}
	m[4].HasAttachments = true
	m[1].Starred = true
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: m}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBody(MessageID("a1", "r002"), BodyUpdate{Text: "Let's discuss the quarterly roadmap at the lake house."}); err != nil {
		t.Fatal(err)
	}
	return s
}

func subjects(rs []models.SearchResult) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Message.Subject)
	}
	return strings.Join(out, "|")
}

func TestSearchPrefixAndRanking(t *testing.T) {
	s := seedSearch(t)
	rs, err := s.Search(models.SearchQuery{Query: "invo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 || rs[0].Message.Subject != "Your invoice for October" || rs[0].MatchedIn != "subject" {
		t.Fatalf("prefix: %s %+v", subjects(rs), rs)
	}
	if rs[0].Highlight != "Your <mark>invo</mark>ice for October" {
		t.Fatalf("highlight %q", rs[0].Highlight)
	}
	// Body search after the body is cached.
	rs, _ = s.Search(models.SearchQuery{Query: "roadmap"})
	if len(rs) != 1 || rs[0].MatchedIn != "body" || !strings.Contains(rs[0].Highlight, "<mark>roadmap</mark>") {
		t.Fatalf("body: %+v", rs)
	}
	// Diacritics-insensitive.
	rs, _ = s.Search(models.SearchQuery{Query: "cafe jose"})
	if len(rs) != 1 || !strings.Contains(rs[0].Highlight, "<mark>Café</mark>") {
		t.Fatalf("diacritics: %+v", rs)
	}
	if !strings.Contains(rs[0].Highlight, "&lt;draft&gt;") {
		t.Fatalf("not escaped: %q", rs[0].Highlight)
	}
}

func TestSearchTypo(t *testing.T) {
	s := seedSearch(t)
	rs, err := s.Search(models.SearchQuery{Query: "invioce"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 || rs[0].Message.Subject != "Your invoice for October" || !rs[0].Fuzzy {
		t.Fatalf("typo: %+v", rs)
	}
	if !strings.Contains(rs[0].Highlight, "<mark>invoice</mark>") {
		t.Fatalf("fuzzy highlight %q", rs[0].Highlight)
	}
	rs, _ = s.Search(models.SearchQuery{Query: "ofsite"})
	if len(rs) == 0 || rs[0].Message.Subject != "Team offsite planning" {
		t.Fatalf("typo2: %s", subjects(rs))
	}
	rs, _ = s.Search(models.SearchQuery{Query: "zzzzqqq"})
	if len(rs) != 0 {
		t.Fatalf("garbage matched %s", subjects(rs))
	}
}

func TestSearchOperators(t *testing.T) {
	s := seedSearch(t)
	cases := map[string]string{
		"from:acme":                          "Payment received|Your invoice for October",
		"from:acme has:attachment":           "Payment received",
		"is:starred":                         "Team offsite planning",
		"in:spam":                            "Weekly newsletter",
		"subject:payment":                    "Payment received",
		`"team offsite"`:                     "Team offsite planning",
		"to:me in:inbox is:unread":           "Team offsite planning",
		"after:2026-10-01 before:2026-10-01": "",
	}
	for q, want := range cases {
		rs, err := s.Search(models.SearchQuery{Query: q})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		got := subjects(rs)
		if q == "from:acme" || q == "to:me in:inbox is:unread" {
			// order depends on rank/recency; compare as a set
			got = sortedJoin(rs)
			want = sortedJoinStr(want)
		}
		if got != want {
			t.Errorf("%q: got %q want %q", q, got, want)
		}
	}
	// Labels.
	s.SetLabels([]models.Label{{ID: "finance", Name: "Money Stuff"}})
	s.AddLabels([]string{MessageID("a1", "r001")}, "finance")
	rs, _ := s.Search(models.SearchQuery{Query: `label:"money stuff"`})
	if subjects(rs) != "Your invoice for October" {
		t.Fatalf("label op %s", subjects(rs))
	}
	rs, _ = s.Search(models.SearchQuery{Query: "acme", Labels: []string{"finance"}})
	if subjects(rs) != "Your invoice for October" {
		t.Fatalf("label filter %s", subjects(rs))
	}
	// ListMessages shares the parser.
	p, err := s.ListMessages(models.ListQuery{Search: "from:acme is:unread"})
	if err != nil || p.Total != 0 {
		// r001 and r005 are odd -> read
		t.Fatalf("list search %v %d", err, p.Total)
	}
	p, _ = s.ListMessages(models.ListQuery{Search: "billing"})
	if p.Total != 2 {
		t.Fatalf("list search total %d", p.Total)
	}
}

func sortedJoin(rs []models.SearchResult) string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Message.Subject)
	}
	return sortedJoinStr(strings.Join(s, "|"))
}

func sortedJoinStr(s string) string {
	parts := strings.Split(s, "|")
	for i := range parts {
		for j := i + 1; j < len(parts); j++ {
			if parts[j] < parts[i] {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	return strings.Join(parts, "|")
}

func TestSearchSpecialCharactersNeverError(t *testing.T) {
	s := seedSearch(t)
	for _, q := range []string{`"`, `""`, `"unterminated`, `(`, `)`, `*`, `-`, `-foo`, `a AND`, `OR`, `NOT x`, `NEAR(a b)`,
		`foo"bar`, `from:`, `from:"`, `subject:(x`, `^`, `{}`, `a:b:c`, `\`, `'`, `%`, `_`, `in:nowhere`, `before:notadate`, `💥`} {
		if _, err := s.Search(models.SearchQuery{Query: q}); err != nil {
			t.Errorf("Search(%q): %v", q, err)
		}
		if _, err := s.ListMessages(models.ListQuery{Search: q}); err != nil {
			t.Errorf("ListMessages(%q): %v", q, err)
		}
	}
}

func TestHighlightEscaping(t *testing.T) {
	h, ok := Highlight(`<script>alert("x")</script> invoice & co`, []hlTerm{{text: "invoice"}}, 0)
	if !ok || h != `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; <mark>invoice</mark> &amp; co` {
		t.Fatalf("%v %q", ok, h)
	}
	long := strings.Repeat("word ", 100) + "needle " + strings.Repeat("tail ", 100)
	h, _ = Highlight(long, []hlTerm{{text: "needle"}}, 80)
	if !strings.HasPrefix(h, "…") || !strings.HasSuffix(h, "…") || !strings.Contains(h, "<mark>needle</mark>") {
		t.Fatalf("window %q", h)
	}
}

func BenchmarkSearch(b *testing.B) {
	s, err := Open(b.TempDir() + "/bench.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	s.InsertAccount(models.Account{ID: "a", Provider: "gmail", Email: "a@x"})
	s.ReplaceFolders("a", []mail.Folder{{RemoteID: "INBOX", Kind: models.FolderInbox, Name: "Inbox"}})
	words := strings.Fields("invoice payment meeting project roadmap lunch report weekly update security alert travel booking order shipped review")
	var batch []mail.Message
	for i := 0; i < 50000; i++ {
		subj := fmt.Sprintf("%s %s %d", words[i%len(words)], words[(i/7)%len(words)], i)
		batch = append(batch, mail.Message{RemoteID: fmt.Sprint(i), Subject: subj, Snippet: subj + " details",
			From: models.Address{Name: fmt.Sprintf("Sender %d", i%300), Email: fmt.Sprintf("s%d@example.com", i%300)},
			Date: time.Now().Add(-time.Duration(i) * time.Minute), FolderRemoteIDs: []string{"INBOX"}})
		if len(batch) == 5000 {
			if _, err := s.ApplySync("a", mail.SyncResult{Upserts: batch}, nil); err != nil {
				b.Fatal(err)
			}
			batch = nil
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, q := range []string{"invo", "roadmap lunch", "invioce", "from:sender is:unread"} {
			if _, err := s.Search(models.SearchQuery{Query: q}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
