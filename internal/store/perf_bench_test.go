package store

// Realistic-volume benchmarks of the store's hot paths: two mailboxes
// (Gmail-like with All Mail, Outlook-like with one folder per message),
// ~50k messages in threads, provider/AI/manual labels and cached bodies.
//
//	go test ./internal/store -run '^$' -bench Perf -benchtime 2s
//	GOMAIL_EXPLAIN=1 go test ./internal/store -run TestPerfExplain -v
//	(GOMAIL_PERF_NOANALYZE=1: without planner statistics)

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

var perfFirst = strings.Fields("Alex Sam Jordan Taylor Casey Riley Morgan Jamie Avery Quinn Drew Robin Charlie Skyler Reese Parker")
var perfLast = strings.Fields("Morgan Lee Rivera Chen Patel Brooks Hayes Foster Reed Bennett Gray Ellis Price Wells Hart Lane")
var perfWords = strings.Fields(`invoice payment meeting project roadmap lunch report weekly update security alert travel booking order
	shipped review quarterly budget design launch release notes hiring offer contract renewal subscription newsletter webinar
	conference agenda minutes feedback survey receipt delivery tracking refund support ticket password account statement
	dinner weekend photos family school holiday flight hotel itinerary reminder deadline proposal draft summary planning
	migration database backend frontend incident postmortem standup retro demo sprint invoice2024 analytics dashboard`)

const (
	perfGmailMsgs   = 30000
	perfOutlookMsgs = 20000
)

type perfFixture struct {
	s                       *Store
	gInbox, gAll, gSent     string
	oInbox, oArchive, oProj string
	someIDs                 []string // 500 message ids across both accounts
	convIDs                 []string // 200 conversation ids from the unified inbox
	updates                 []mail.Message
}

var (
	perfOnce sync.Once
	perfFix  *perfFixture
	perfErr  error
	perfDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if perfFix != nil {
		perfFix.s.Close()
	}
	if perfDir != "" {
		os.RemoveAll(perfDir)
	}
	os.Exit(code)
}

func perfSender(r *rand.Rand) models.Address {
	// Skewed: a few senders send most of the mail.
	n := int(r.ExpFloat64()*120) % 2000
	f, l := perfFirst[n%len(perfFirst)], perfLast[(n/len(perfFirst))%len(perfLast)]
	return models.Address{Name: f + " " + l, Email: fmt.Sprintf("%s.%s%d@example.com", strings.ToLower(f), strings.ToLower(l), n)}
}

func perfText(r *rand.Rand, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = perfWords[r.IntN(len(perfWords))]
	}
	return strings.Join(w, " ")
}

// perfMessages generates one mailbox. folderOf decides the folders of
// message i of a thread (rank 0..1 = newest..oldest).
func perfMessages(r *rand.Rand, prefix string, n int, me models.Address, folderOf func(rank float64, sent bool) []string, labels []string) []mail.Message {
	out := make([]mail.Message, 0, n)
	span := 4 * 365 * 24 * time.Hour
	base := time.Now().Add(-time.Minute)
	t := 0
	for len(out) < n {
		size := 1 + int(r.ExpFloat64()*1.2)
		if size > 12 {
			size = 12
		}
		rank := float64(len(out)) / float64(n)
		start := base.Add(-time.Duration(rank * float64(span)))
		subj := perfText(r, 3+r.IntN(4))
		other := perfSender(r)
		var lbl []string
		switch p := r.Float64(); {
		case p < 0.10:
			lbl = []string{labels[r.IntN(len(labels))], labels[r.IntN(len(labels))]}
		case p < 0.45:
			lbl = []string{labels[r.IntN(len(labels))]}
		}
		thread := fmt.Sprintf("%s-t%d", prefix, t)
		t++
		for j := 0; j < size && len(out) < n; j++ {
			sent := j > 0 && r.Float64() < 0.3
			from, to := other, []models.Address{me}
			if sent {
				from, to = me, []models.Address{other}
			}
			i := len(out)
			m := mail.Message{
				RemoteID: fmt.Sprintf("%s%d", prefix, i), ThreadID: thread, Subject: subj, Snippet: perfText(r, 14),
				From: from, To: to, Date: start.Add(-time.Duration(size-j) * 17 * time.Minute),
				FolderRemoteIDs: folderOf(rank, sent), Labels: lbl,
				Starred: r.Float64() < 0.02, HasAttachments: r.Float64() < 0.12,
			}
			if j > 0 {
				m.Subject = "Re: " + subj
			}
			out = append(out, m)
		}
	}
	return out
}

func contains2(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func buildPerfFixture() (*perfFixture, error) {
	dir, err := os.MkdirTemp("", "gomail-perf-")
	if err != nil {
		return nil, err
	}
	perfDir = dir
	s, err := Open(filepath.Join(dir, "perf.db"))
	if err != nil {
		return nil, err
	}
	f := &perfFixture{s: s}
	r := rand.New(rand.NewPCG(1, 2))
	for _, a := range []models.Account{{ID: "g", Provider: "gmail", Email: "me@example.com"}, {ID: "o", Provider: "outlook", Email: "alex.morgan@example.org"}} {
		if err := s.InsertAccount(a); err != nil {
			return nil, err
		}
	}
	if err := s.ReplaceFolders("g", []mail.Folder{{RemoteID: "INBOX", Kind: models.FolderInbox, Name: "Inbox", Total: 3500},
		{RemoteID: "ALL", Kind: models.FolderArchive, Name: "All Mail", Total: perfGmailMsgs}, {RemoteID: "SENT", Kind: models.FolderSent, Name: "Sent"},
		{RemoteID: "TRASH", Kind: models.FolderTrash, Name: "Trash"}, {RemoteID: "SPAM", Kind: models.FolderSpam, Name: "Spam"},
		{RemoteID: "STARRED", Kind: models.FolderStarred, Name: "Starred"}}); err != nil {
		return nil, err
	}
	if err := s.ReplaceFolders("o", []mail.Folder{{RemoteID: "in", Kind: models.FolderInbox, Name: "Inbox", Total: 3500},
		{RemoteID: "arch", Kind: models.FolderArchive, Name: "Archive"}, {RemoteID: "sent", Kind: models.FolderSent, Name: "Sent Items"},
		{RemoteID: "del", Kind: models.FolderTrash, Name: "Deleted Items"}, {RemoteID: "junk", Kind: models.FolderSpam, Name: "Junk Email"},
		{RemoteID: "proj", Kind: models.FolderCustom, Name: "Projects"}, {RemoteID: "rcpt", Kind: models.FolderCustom, Name: "Receipts"}}); err != nil {
		return nil, err
	}
	gLabels := []string{"Work", "Finance", "Travel", "Family", "Receipts", "Newsletters"}
	oLabels := []string{"Red category", "Blue category", "Green category", "Clients"}
	labelIDs := map[string]map[string]string{"g": {}, "o": {}}
	var defs []models.Label
	for _, l := range gLabels {
		labelIDs["g"][strings.ToLower(l)] = "g-" + strings.ToLower(l)
		defs = append(defs, models.Label{ID: "g-" + strings.ToLower(l), Name: l})
	}
	for _, l := range oLabels {
		id := "o-" + strings.ReplaceAll(strings.ToLower(l), " ", "-")
		labelIDs["o"][strings.ToLower(l)] = id
		defs = append(defs, models.Label{ID: id, Name: l})
	}
	defs = append(defs, models.Label{ID: "ai-news", Name: "Newsletter (AI)"}, models.Label{ID: "ai-promo", Name: "Promotions"}, models.Label{ID: "important", Name: "Important"})
	s.SetLabels(defs)

	me := models.Address{Name: "Me", Email: "me@example.com"}
	gmail := perfMessages(r, "g", perfGmailMsgs, me, func(rank float64, sent bool) []string {
		switch p := r.Float64(); {
		case p < 0.02:
			return []string{"TRASH"}
		case p < 0.03:
			return []string{"SPAM"}
		case sent:
			return []string{"SENT", "ALL"}
		case (rank < 0.1 && p < 0.5) || p < 0.08:
			return []string{"INBOX", "ALL"}
		}
		return []string{"ALL"}
	}, gLabels)
	me2 := models.Address{Name: "Alex Morgan", Email: "alex.morgan@example.org"}
	outlook := perfMessages(r, "o", perfOutlookMsgs, me2, func(rank float64, sent bool) []string {
		switch p := r.Float64(); {
		case sent:
			return []string{"sent"}
		case p < 0.015:
			return []string{"del"}
		case p < 0.025:
			return []string{"junk"}
		case (rank < 0.15 && p < 0.6) || p < 0.1:
			return []string{"in"}
		case p < 0.2:
			return []string{"proj"}
		case p < 0.27:
			return []string{"rcpt"}
		}
		return []string{"arch"}
	}, oLabels)
	for _, mb := range []struct {
		acct string
		msgs []mail.Message
	}{{"g", gmail}, {"o", outlook}} {
		for off := 0; off < len(mb.msgs); off += 5000 {
			batch := mb.msgs[off:min(off+5000, len(mb.msgs))]
			for i := range batch {
				// Unread: a fifth of the inbox, a little elsewhere.
				inInbox := contains2(batch[i].FolderRemoteIDs, "INBOX") || contains2(batch[i].FolderRemoteIDs, "in")
				batch[i].Unread = (inInbox && r.Float64() < 0.2) || r.Float64() < 0.03
			}
			if _, err := s.ApplySync(mb.acct, mail.SyncResult{Upserts: batch}, labelIDs[mb.acct]); err != nil {
				return nil, err
			}
		}
	}
	// AI and manual labels, cached bodies for a quarter of the mail.
	for _, q := range []string{
		`INSERT OR IGNORE INTO message_labels(message_id, label_id, source, confidence, created_at) SELECT id, 'ai-news', 'ai', 0.8, 1 FROM messages WHERE rid % 7 = 0`,
		`INSERT OR IGNORE INTO message_labels(message_id, label_id, source, confidence, created_at) SELECT id, 'ai-promo', 'ai', 0.7, 1 FROM messages WHERE rid % 11 = 3`,
		`INSERT OR IGNORE INTO message_labels(message_id, label_id, source, confidence, created_at) SELECT id, 'important', 'manual', 1, 1 FROM messages WHERE rid % 97 = 5`,
		// Opened mail keeps its sanitized HTML (with embedded images) in the
		// row: 20-44 KB here, so later columns sit on overflow pages.
		`UPDATE messages SET body_text = subject || ' ' || snippet || ' ' || snippet || ' regards, the team', body_fetched_at = 1, classified_at = 1,
			body_html = '<div>' || hex(zeroblob(10000 + (rid % 7) * 2000)) || '</div>' WHERE rid % 4 = 0`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	// Real databases may never have been analyzed: GOMAIL_PERF_NOANALYZE=1
	// benchmarks the planner without statistics.
	if os.Getenv("GOMAIL_PERF_NOANALYZE") == "" {
		if _, err := s.db.Exec(`ANALYZE`); err != nil {
			return nil, err
		}
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return nil, err
	}
	f.gInbox, f.gAll, f.gSent = FolderID("g", "INBOX"), FolderID("g", "ALL"), FolderID("g", "SENT")
	f.oInbox, f.oArchive, f.oProj = FolderID("o", "in"), FolderID("o", "arch"), FolderID("o", "proj")
	for i := 0; i < 250; i++ {
		f.someIDs = append(f.someIDs, MessageID("g", gmail[i*97].RemoteID), MessageID("o", outlook[i*61].RemoteID))
	}
	p, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox, Limit: 200}, ListOptions{Conversations: true})
	if err != nil {
		return nil, err
	}
	for _, m := range p.Messages {
		f.convIDs = append(f.convIDs, m.ConversationID)
	}
	// An incremental sync round: 500 known Gmail messages with changed flags.
	for i := 0; i < 500; i++ {
		m := gmail[i*7]
		m.Unread = !m.Unread
		f.updates = append(f.updates, m)
	}
	return f, nil
}

func perfDB(b testing.TB) *perfFixture {
	perfOnce.Do(func() {
		start := time.Now()
		perfFix, perfErr = buildPerfFixture()
		if perfErr == nil {
			fmt.Fprintf(os.Stderr, "perf fixture: %d+%d messages in %v\n", perfGmailMsgs, perfOutlookMsgs, time.Since(start).Round(time.Millisecond))
		}
	})
	if perfErr != nil {
		b.Fatal(perfErr)
	}
	return perfFix
}

// perfQueries are the list views the UI asks for most.
func perfQueries(f *perfFixture) []struct {
	name string
	q    models.ListQuery
} {
	return []struct {
		name string
		q    models.ListQuery
	}{
		{"inbox", models.ListQuery{AccountID: "g", FolderID: f.gInbox}},
		{"inbox-outlook", models.ListQuery{AccountID: "o", FolderID: f.oInbox}},
		{"unified-inbox", models.ListQuery{FolderKind: models.FolderInbox}},
		{"allmail", models.ListQuery{AccountID: "g", FolderID: f.gAll}},
		{"custom-folder", models.ListQuery{AccountID: "o", FolderID: f.oProj}},
		{"unread", models.ListQuery{FolderKind: models.FolderInbox, UnreadOnly: true}},
		{"label-inbox", models.ListQuery{AccountID: "g", FolderID: f.gInbox, Labels: []string{"g-work"}}},
		{"label-allmail", models.ListQuery{AccountID: "g", FolderID: f.gAll, Labels: []string{"g-travel", "ai-news"}}},
		{"label-unified", models.ListQuery{Labels: []string{"important"}}},
		{"exclude-labels", models.ListQuery{FolderKind: models.FolderInbox, ExcludeLabels: []string{"ai-news", "ai-promo"}}},
		{"unlabeled", models.ListQuery{AccountID: "g", FolderID: f.gInbox, Unlabeled: true}},
		{"label-unread", models.ListQuery{FolderKind: models.FolderInbox, Labels: []string{"g-work", "o-clients"}, UnreadOnly: true}},
		{"search-list", models.ListQuery{FolderKind: models.FolderInbox, Search: "invoice"}},
	}
}

// BenchmarkPerfList measures a fresh first page as the UI loads it after a
// change (the count cache is invalidated each time, like after a write).
func BenchmarkPerfList(b *testing.B) {
	f := perfDB(b)
	for _, conv := range []bool{true, false} {
		view := "plain"
		if conv {
			view = "conv"
		}
		for _, tc := range perfQueries(f) {
			b.Run(view+"/"+tc.name, func(b *testing.B) {
				for b.Loop() {
					f.s.gen.Add(1)
					if _, err := f.s.ListMessages(tc.q, ListOptions{Conversations: conv}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkPerfListPage3 is infinite scroll: the third page (counts cached).
func BenchmarkPerfListPage3(b *testing.B) {
	f := perfDB(b)
	for _, tc := range perfQueries(f)[:4] {
		q := tc.q
		q.Limit = 60
		p1, _ := f.s.ListMessages(q, ListOptions{Conversations: true})
		q.Cursor = p1.NextCursor
		p2, _ := f.s.ListMessages(q, ListOptions{Conversations: true})
		q.Cursor = p2.NextCursor
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.ListMessages(q, ListOptions{Conversations: true}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerfCount measures the list totals alone.
func BenchmarkPerfCount(b *testing.B) {
	f := perfDB(b)
	for _, tc := range perfQueries(f) {
		b.Run("conv/"+tc.name, func(b *testing.B) {
			for b.Loop() {
				f.s.gen.Add(1)
				w, err := f.s.buildFilter(tc.q, FilterOptions{})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := f.s.listTotal(tc.q, ListOptions{Conversations: true}, w); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPerfResolveIDs(b *testing.B) {
	f := perfDB(b)
	for _, tc := range perfQueries(f) {
		if tc.q.Search != "" {
			continue
		}
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.ResolveIDs(tc.q, FilterOptions{}, 100000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPerfSearch(b *testing.B) {
	f := perfDB(b)
	for _, q := range []string{"invo", "roadmap lunch", "invioce", "from:alex is:unread", "\"weekly update\"", "label:work after:2024-01-01", "has:attachment in:inbox report", "subject:budget"} {
		b.Run(q, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.Search(models.SearchQuery{Query: q}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPerfLabelCounts(b *testing.B) {
	f := perfDB(b)
	for _, acct := range []string{"", "g"} {
		b.Run("account="+acct, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.LabelCounts(acct); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerfViewLabelCounts is the label sidebar within a box.
func BenchmarkPerfViewLabelCounts(b *testing.B) {
	f := perfDB(b)
	for _, tc := range perfQueries(f)[:5] {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.ViewLabelCounts(tc.q); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPerfApplySync(b *testing.B) {
	f := perfDB(b)
	ids := map[string]string{"work": "g-work", "finance": "g-finance", "travel": "g-travel", "family": "g-family", "receipts": "g-receipts", "newsletters": "g-newsletters"}
	b.Run("update500", func(b *testing.B) {
		for b.Loop() {
			for i := range f.updates {
				f.updates[i].Unread = !f.updates[i].Unread
			}
			if _, err := f.s.ApplySync("g", mail.SyncResult{Upserts: f.updates}, ids); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("noop500", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.ApplySync("g", mail.SyncResult{Upserts: f.updates}, ids); err != nil {
				b.Fatal(err)
			}
		}
	})
	// New mail, then the same 100 deleted / moved out of a big folder.
	r := rand.New(rand.NewPCG(3, 4))
	n := 0
	batch := func() ([]mail.Message, []string) {
		msgs := perfMessages(r, fmt.Sprintf("bf%d-", n), 100, models.Address{Email: "me@example.com"}, func(float64, bool) []string { return []string{"INBOX", "ALL"} }, []string{"Work"})
		n++
		rids := make([]string, len(msgs))
		for i, m := range msgs {
			rids[i] = m.RemoteID
		}
		return msgs, rids
	}
	apply := func(b *testing.B, r mail.SyncResult) {
		if _, err := f.s.ApplySync("g", r, ids); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("backfill100", func(b *testing.B) {
		for b.Loop() {
			msgs, rids := batch()
			apply(b, mail.SyncResult{Upserts: msgs})
			b.StopTimer()
			apply(b, mail.SyncResult{Deleted: rids})
			b.StartTimer()
		}
	})
	b.Run("delete100", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			msgs, rids := batch()
			apply(b, mail.SyncResult{Upserts: msgs})
			b.StartTimer()
			apply(b, mail.SyncResult{Deleted: rids})
		}
	})
	b.Run("archive100", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			msgs, rids := batch()
			apply(b, mail.SyncResult{Upserts: msgs})
			b.StartTimer()
			apply(b, mail.SyncResult{Removed: map[string][]string{"INBOX": rids}})
			b.StopTimer()
			apply(b, mail.SyncResult{Deleted: rids})
			b.StartTimer()
		}
	})
	b.Run("empty", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.ApplySync("g", mail.SyncResult{}, ids); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPerfClassifyInputs(b *testing.B) {
	f := perfDB(b)
	for b.Loop() {
		if in, err := f.s.ClassifyInputs(f.someIDs); err != nil || len(in) != len(f.someIDs) {
			b.Fatal(err, len(in))
		}
	}
}

func BenchmarkPerfRuleCandidates(b *testing.B) {
	f := perfDB(b)
	b.Run("500", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.RuleCandidates(f.someIDs); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("all", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.AllRuleCandidates(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPerfExpandConversations(b *testing.B) {
	f := perfDB(b)
	for _, mode := range []struct {
		name string
		m    ExpandMode
	}{{"thread", ExpandThread}, {"folder", ExpandFolder}} {
		b.Run(mode.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := f.s.ExpandConversations(f.convIDs, mode.m); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPerfSummaries(b *testing.B) {
	f := perfDB(b)
	for b.Loop() {
		if _, err := f.s.Summaries(f.someIDs); err != nil {
			b.Fatal(err)
		}
	}
}

// TestPerfExplain prints the query plans of the list views (opt-in).
func TestPerfExplain(t *testing.T) {
	if os.Getenv("GOMAIL_EXPLAIN") == "" {
		t.Skip("set GOMAIL_EXPLAIN=1")
	}
	f := perfDB(t)
	explain := func(name, q string, args []any) {
		rows, err := f.s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			b.WriteString("    " + detail + "\n")
		}
		t.Logf("%s\n%s", name, b.String())
	}
	for _, tc := range perfQueries(f) {
		for _, conv := range []bool{true, false} {
			w, err := f.s.buildFilter(tc.q, FilterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := f.s.fastConversationCount(tc.q, ListOptions{Conversations: conv}); !ok {
				expr := `count(*)`
				if conv {
					expr = convCountExpr
				}
				explain(fmt.Sprintf("count conv=%v %s", conv, tc.name), `SELECT `+expr+` FROM messages m`+w.sql(), w.args)
			}
			if conv {
				latestInThread(w)
			}
			explain(fmt.Sprintf("page conv=%v %s", conv, tc.name), `SELECT `+f.s.summaryCols()+` FROM messages m`+w.sql()+` ORDER BY m.date DESC, m.id DESC LIMIT 61`, w.args)
		}
	}
}
