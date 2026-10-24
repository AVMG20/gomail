package store

import (
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
	p, err := s.ListMessages(models.ListQuery{Search: "from:acme is:unread"}, ListOptions{})
	if err != nil || p.Total != 0 {
		// r001 and r005 are odd -> read
		t.Fatalf("list search %v %d", err, p.Total)
	}
	p, _ = s.ListMessages(models.ListQuery{Search: "billing"}, ListOptions{})
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
		if _, err := s.ListMessages(models.ListQuery{Search: q}, ListOptions{}); err != nil {
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

// The list's search box finds the same close matches as the search
// palette when the words as typed match nothing, and says so; "select all"
// covers what the list shows.
func TestListSearchTypo(t *testing.T) {
	s := seedSearch(t)
	p, err := s.ListMessages(models.ListQuery{Search: "invioce"}, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 || len(p.Messages) != 1 || p.Messages[0].Subject != "Your invoice for October" || !p.Similar {
		t.Fatalf("typo in the list: total %d similar %v %+v", p.Total, p.Similar, p.Messages)
	}
	if ids, _ := s.ResolveIDs(models.ListQuery{Search: "invioce"}, FilterOptions{}, 10); len(ids) != 1 {
		t.Fatalf("select all on a typo: %v", ids)
	}
	if p, _ := s.ListMessages(models.ListQuery{Search: "invoice"}, ListOptions{}); p.Similar || p.Total != 1 {
		t.Fatalf("exact search marked similar: %+v", p)
	}
	if p, _ := s.ListMessages(models.ListQuery{Search: "zzzzqqq"}, ListOptions{}); p.Total != 0 || p.Similar {
		t.Fatalf("garbage matched: %+v", p)
	}
}

// The search pool is the most recent hits (newest first, by id on equal
// dates), whether there are few hits (read by rowid) or more than the pool
// (date-order walk).
func TestSearchPoolIsMostRecentHits(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	var ms []mail.Message
	for i := range ftsPool + 100 {
		subject := "invoice"
		if i%150 == 0 {
			subject = "invoice overdue"
		}
		m := msg(i, subject, "Acme", "ALL")
		if i%4 == 0 {
			m.FolderRemoteIDs = []string{"INBOX", "ALL"}
		}
		m.Date = base.Add(time.Duration(i/3) * time.Hour) // equal dates: ordered by id
		ms = append(ms, m)
	}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: ms}, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"invo", ftsPool}, {"invoice in:inbox", (ftsPool + 100) / 4}, {"overdue", 5}, {"zebra", 0}} {
		ps := ParseSearch(tc.query)
		w := &where{}
		s.addSearchFilters(w, ps)
		w.add(visible("m"))
		cands, err := s.ftsCandidates(ps.FTSQuery(), w, ps.highlightTerms(), now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range cands {
			got = append(got, c.sum.ID)
		}
		rows, err := s.db.Query(`SELECT m.id FROM messages m WHERE m.rid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?) AND `+
			strings.Join(w.conds, " AND ")+` ORDER BY m.date DESC, m.id DESC LIMIT ?`, append(append([]any{ps.FTSQuery()}, w.args...), ftsPool)...)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want = append(want, id)
		}
		rows.Close()
		if len(want) != tc.want || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%q: got %d hits, want %d of %d (in the same order)", tc.query, len(got), tc.want, len(want))
		}
	}
}
