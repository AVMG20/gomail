package store

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func threadMsg(id, thread string, h int, from string, folders ...string) mail.Message {
	return mail.Message{RemoteID: id, ThreadID: thread, FolderRemoteIDs: folders, Subject: "S " + thread,
		From: models.Address{Name: from, Email: strings.ToLower(from) + "@example.com"}, Date: base.Add(time.Duration(h) * time.Hour)}
}

func TestConversationView(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	if err := s.ReplaceFolders("a1", []mail.Folder{
		{RemoteID: "INBOX", Name: "Inbox", Kind: models.FolderInbox}, {RemoteID: "SENT", Name: "Sent", Kind: models.FolderSent},
		{RemoteID: "ALL", Name: "All Mail", Kind: models.FolderArchive}, {RemoteID: "TRASH", Name: "Trash", Kind: models.FolderTrash},
		{RemoteID: "SPAM", Name: "Spam", Kind: models.FolderSpam},
	}); err != nil {
		t.Fatal(err)
	}
	me := threadMsg("t1-me", "T1", 2, "A1", "SENT", "ALL") // the account itself (a1@example.com)
	ms := []mail.Message{
		threadMsg("t1-a", "T1", 1, "Ann", "INBOX", "ALL"),
		me,
		threadMsg("t1-b", "T1", 3, "Bob", "INBOX", "ALL"),
		threadMsg("t2-a", "T2", 4, "Cid", "INBOX", "ALL"),
		threadMsg("t3-a", "", 5, "Dee", "INBOX", "ALL"), // no thread id: its own conversation
		threadMsg("t1-x", "T1", 6, "Eve", "TRASH"),      // trashed message doesn't join the inbox thread
	}
	ms[0].Unread = true
	ms[0].HasAttachments = true
	ms[3].Starred = true
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: ms}, nil); err != nil {
		t.Fatal(err)
	}
	s.AddLabels([]string{MessageID("a1", "t1-a")}, "work")
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)

	p, err := s.ListMessages(models.ListQuery{FolderID: inbox.ID}, ListOptions{Conversations: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 3 || len(p.Messages) != 3 {
		t.Fatalf("total %d len %d", p.Total, len(p.Messages))
	}
	t1 := p.Messages[2]
	if t1.RemoteID != "t1-b" || t1.ThreadCount != 3 || !t1.Unread || !t1.HasAttachments || fmt.Sprint(t1.Labels) != "[work]" {
		t.Fatalf("t1 row %+v", t1)
	}
	if fmt.Sprint(t1.Participants) != "[Ann me Bob]" || t1.ConversationID != "c:"+t1.ID {
		t.Fatalf("participants %v id %s", t1.Participants, t1.ConversationID)
	}
	// Keyset pagination over conversations.
	var seen []string
	cursor := ""
	for {
		pg, err := s.ListMessages(models.ListQuery{FolderID: inbox.ID, Limit: 1, Cursor: cursor}, ListOptions{Conversations: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pg.Messages {
			seen = append(seen, m.RemoteID)
		}
		if pg.NextCursor == "" {
			break
		}
		cursor = pg.NextCursor
	}
	if strings.Join(seen, ",") != "t3-a,t2-a,t1-b" {
		t.Fatalf("pages %v", seen)
	}
	// Message view is unchanged.
	p, _ = s.ListMessages(models.ListQuery{FolderID: inbox.ID}, ListOptions{})
	if p.Total != 4 || p.Messages[0].ThreadCount != 1 {
		t.Fatalf("message view %d", p.Total)
	}
	// Unread filter picks the conversation by its unread message.
	p, _ = s.ListMessages(models.ListQuery{FolderID: inbox.ID, UnreadOnly: true}, ListOptions{Conversations: true})
	if p.Total != 1 || p.Messages[0].RemoteID != "t1-a" || p.Messages[0].ThreadCount != 3 {
		t.Fatalf("unread conv %+v", p.Messages)
	}

	cid := "c:" + MessageID("a1", "t1-b")
	all, _ := s.ExpandConversations([]string{cid}, ExpandThread)
	if len(all) != 3 {
		t.Fatalf("thread expand %v", all)
	}
	inFolder, _ := s.ExpandConversations([]string{cid, MessageID("a1", "t2-a")}, ExpandFolder)
	if len(inFolder) != 3 { // t1-b, t1-a (inbox), t2-a; not the sent reply
		t.Fatalf("folder expand %v", inFolder)
	}
	for _, id := range inFolder {
		if id == MessageID("a1", "t1-me") {
			t.Fatal("sent reply included in folder expansion")
		}
	}
	anchor, _ := s.ExpandConversations([]string{cid}, ExpandAnchor)
	if len(anchor) != 1 || anchor[0] != MessageID("a1", "t1-b") {
		t.Fatal(anchor)
	}
	// Trash view: the trashed message is its own conversation.
	trash, _ := s.FolderByKind("a1", models.FolderTrash)
	p, _ = s.ListMessages(models.ListQuery{FolderID: trash.ID}, ListOptions{Conversations: true})
	if p.Total != 1 || p.Messages[0].ThreadCount != 1 {
		t.Fatalf("trash %+v", p.Messages)
	}
}

func TestHasMoreRemote(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1") // inbox server total 10
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{msg(1, "x", "A", "INBOX", "ALL")}}, nil)
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	p, _ := s.ListMessages(models.ListQuery{FolderID: inbox.ID}, ListOptions{})
	if !p.HasMoreRemote {
		t.Fatal("expected more remote")
	}
	old, _ := s.OldestInFolder(inbox.ID)
	if !old.Equal(base.Add(time.Hour)) {
		t.Fatal(old)
	}
	s.SetOlderDone("a1", inbox.ID, true)
	p, _ = s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox}, ListOptions{})
	if p.HasMoreRemote {
		t.Fatal("older marked done")
	}
	p, _ = s.ListMessages(models.ListQuery{FolderID: inbox.ID, Search: "x"}, ListOptions{})
	if p.HasMoreRemote {
		t.Fatal("search views never report more remote")
	}
}

func cutoffAt(t time.Time) func(string) time.Time { return func(string) time.Time { return t } }

// Clearing the inbox must not pull in mail past the sync depth: once the
// cached mail reaches below the cutoff, older mail is offered, not fetched.
func TestHasMoreRemoteRespectsSyncDepth(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1") // inbox server total 10
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{msg(1, "x", "A", "INBOX", "ALL")}}, nil)
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	oldest, _ := s.OldestInFolder(inbox.ID)
	q := models.ListQuery{FolderID: inbox.ID}

	p, _ := s.ListMessages(q, ListOptions{OlderCutoff: cutoffAt(oldest.Add(-time.Hour))})
	if !p.HasMoreRemote || p.OlderBeyondDepth {
		t.Fatalf("within the sync depth: %+v", p)
	}
	p, _ = s.ListMessages(q, ListOptions{OlderCutoff: cutoffAt(oldest.Add(time.Hour))})
	if p.HasMoreRemote || !p.OlderBeyondDepth {
		t.Fatalf("past the sync depth: %+v", p)
	}
	s.SetOlderDone("a1", inbox.ID, true)
	p, _ = s.ListMessages(q, ListOptions{OlderCutoff: cutoffAt(oldest.Add(time.Hour))})
	if p.HasMoreRemote || p.OlderBeyondDepth {
		t.Fatalf("nothing older on the server: %+v", p)
	}
}

// TestConversationCountersStayConsistent checks the trigger-maintained
// folder_threads counters against a full recount after many operations.
func TestConversationCountersStayConsistent(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	var ms []mail.Message
	for i := 0; i < 60; i++ {
		m := threadMsg(fmt.Sprint("r", i), fmt.Sprint("T", i/4), i, "X", "INBOX", "ALL")
		m.Unread = i%3 == 0
		if i%10 == 0 {
			m.ThreadID = ""
		}
		ms = append(ms, m)
	}
	s.ApplySync("a1", mail.SyncResult{Upserts: ms}, nil)
	check := func(step string) {
		t.Helper()
		folders, _ := s.Folders("a1")
		for _, f := range folders {
			if f.Kind == models.FolderStarred {
				continue
			}
			for _, unread := range []bool{false, true} {
				q := models.ListQuery{FolderID: f.ID, UnreadOnly: unread}
				fast, ok, err := s.fastConversationCount(q, ListOptions{Conversations: true})
				slow, err2 := s.slowConversationCount(q)
				if err != nil || err2 != nil || !ok || fast != slow {
					t.Fatalf("%s: folder %s unread=%v fast=%d slow=%d (%v %v)", step, f.Name, unread, fast, slow, err, err2)
				}
			}
		}
	}
	check("initial")
	ids := []string{MessageID("a1", "r1"), MessageID("a1", "r2"), MessageID("a1", "r3"), MessageID("a1", "r30")}
	s.SetUnread(ids, true)
	check("unread")
	s.SetUnread(ids[:2], false)
	check("read")
	spam, _ := s.FolderByKind("a1", models.FolderSpam)
	s.SetMessageFolders(map[string][]string{ids[0]: {spam.ID}, ids[3]: {}})
	check("move")
	ms[5].ThreadID = "T99" // re-threaded by the provider
	ms[6].Unread = !ms[6].Unread
	s.ApplySync("a1", mail.SyncResult{Upserts: ms[5:7], Deleted: []string{"r7", "r8"}, Removed: map[string][]string{"INBOX": {"r9"}}}, nil)
	check("resync")
	s.DeleteAccount("a1")
	var left int
	s.db.QueryRow(`SELECT count(*) FROM folder_threads`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d counters left after deleting the account", left)
	}
}

// The sidebar's label counts follow the box on screen: clicking a label
// filters that box, so Inbox and All Mail count differently.
func TestViewLabelCounts(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{
		msg(0, "in inbox", "A", "INBOX", "ALL"), // unread
		msg(1, "archived", "A", "ALL"),
		msg(2, "archived too", "A", "ALL"), // unread
	}}, nil)
	ids := []string{MessageID("a1", "r000"), MessageID("a1", "r001"), MessageID("a1", "r002")}
	if err := s.AddLabels(ids, "news"); err != nil {
		t.Fatal(err)
	}
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	count := func(q models.ListQuery) models.LabelCount {
		t.Helper()
		cs, err := s.ViewLabelCounts(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.LabelID == "news" {
				return c
			}
		}
		return models.LabelCount{}
	}
	if c := count(models.ListQuery{AccountID: "a1", FolderID: inbox.ID}); c.Total != 1 || c.Unread != 1 {
		t.Fatalf("inbox %+v", c)
	}
	if c := count(models.ListQuery{FolderKind: models.FolderArchive}); c.Total != 3 || c.Unread != 2 {
		t.Fatalf("all mail %+v", c)
	}
	// The list's own filters don't change the counts.
	if c := count(models.ListQuery{AccountID: "a1", FolderID: inbox.ID, Labels: []string{"other"}, Search: "zzz", UnreadOnly: true}); c.Total != 1 {
		t.Fatalf("filtered inbox %+v", c)
	}
}

// aggregateThreadsPerRow is the reference for aggregateThreads: two queries
// per conversation row.
func aggregateThreadsPerRow(s *Store, rows []models.MessageSummary) error {
	for i := range rows {
		r := &rows[i]
		r.ConversationID = ConversationPrefix + r.ID
		rs, err := s.db.Query(`SELECT t.unread, t.starred, t.has_attachments, t.from_name, t.from_email, a.email
			FROM messages x JOIN messages t ON `+sameThread+` JOIN accounts a ON a.id = t.account_id
			WHERE x.id = ? ORDER BY t.date, t.id`, r.ID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		r.Participants = []string{}
		count := 0
		for rs.Next() {
			var unread, starred, att int
			var name, email, me string
			if err := rs.Scan(&unread, &starred, &att, &name, &email, &me); err != nil {
				rs.Close()
				return err
			}
			count++
			r.Unread = r.Unread || unread == 1
			r.Starred = r.Starred || starred == 1
			r.HasAttachments = r.HasAttachments || att == 1
			p := displayName(models.Address{Name: name, Email: email})
			if strings.EqualFold(email, me) {
				p = "me"
			}
			if !seen[p] && len(r.Participants) < 6 {
				seen[p] = true
				r.Participants = append(r.Participants, p)
			}
		}
		rs.Close()
		r.ThreadCount = max(count, 1)
		lr, err := s.db.Query(`SELECT ml.label_id, ml.source, max(ml.confidence)
			FROM messages x JOIN messages t ON `+sameThread+` JOIN message_labels ml ON ml.message_id = t.id
			WHERE x.id = ?`+s.labelCond()+` GROUP BY ml.label_id, ml.source`, r.ID)
		if err != nil {
			return err
		}
		best := map[string]models.MessageLabel{}
		var order []string
		for lr.Next() {
			var l models.MessageLabel
			if err := lr.Scan(&l.LabelID, &l.Source, &l.Confidence); err != nil {
				lr.Close()
				return err
			}
			cur, ok := best[l.LabelID]
			if !ok {
				order = append(order, l.LabelID)
			}
			if !ok || sourceRank[l.Source] > sourceRank[cur.Source] {
				best[l.LabelID] = l
			}
		}
		lr.Close()
		if len(order) > 0 || len(r.Labels) > 0 {
			ids, details := []string{}, []models.MessageLabel{}
			for _, id := range sortedStrings(order) {
				ids = append(ids, id)
				details = append(details, best[id])
			}
			r.Labels, r.LabelDetails = ids, details
		}
	}
	return nil
}

// A page's conversation rows are aggregated in two queries for the whole
// page; they must come out exactly as thread by thread.
func TestAggregateThreadsMatchesPerRow(t *testing.T) {
	s := openTest(t)
	r := rand.New(rand.NewPCG(1, 2))
	folders := [][]string{{"INBOX", "ALL"}, {"ALL"}, {"TRASH"}, {"SPAM"}, {"INBOX", "ALL"}, {"INBOX", "ALL"}}
	senders := []string{"Ann", "Bob", "Cid", "Dee", "Eve", "Fay", "Gus", "Hal", "A1", "A2"}
	sources := []string{"manual", "provider", "system", "ai", "rule", "import"} // the last two tie
	for _, acct := range []string{"a1", "a2"} {
		seedAccount(t, s, acct)
		var ms []mail.Message
		for i := range 150 {
			thread := fmt.Sprint("T", r.IntN(30))
			if r.IntN(8) == 0 {
				thread = "" // its own conversation
			}
			// Few distinct hours: equal dates exercise the id tie-break.
			m := threadMsg(fmt.Sprint(acct, "-", i), thread, r.IntN(60), senders[r.IntN(len(senders))], folders[r.IntN(len(folders))]...)
			if r.IntN(10) == 0 {
				m.From.Name = "" // shown by the email's user part
			}
			m.Unread, m.Starred, m.HasAttachments = r.IntN(3) == 0, r.IntN(7) == 0, r.IntN(5) == 0
			ms = append(ms, m)
		}
		if _, err := s.ApplySync(acct, mail.SyncResult{Upserts: ms}, nil); err != nil {
			t.Fatal(err)
		}
		for i := range 150 {
			for _, label := range []string{"work", "news", "travel"} {
				if r.IntN(4) == 0 {
					if _, err := s.db.Exec(`INSERT INTO message_labels(message_id, label_id, source, confidence) VALUES(?, ?, ?, ?)`,
						MessageID(acct, fmt.Sprint(acct, "-", i)), label, sources[r.IntN(len(sources))], float64(r.IntN(100))/100); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	s.SetLabels([]models.Label{{ID: "travel", Name: "Travel", Source: config.LabelSourceProvider}})
	for _, hide := range []bool{false, true} {
		s.SetHideProviderLabels(hide)
		for _, q := range []models.ListQuery{{}, {FolderKind: models.FolderInbox}, {FolderKind: models.FolderTrash}, {AccountID: "a2", Labels: []string{"news"}}} {
			q.Limit = 7
			pages := 0
			for {
				p, err := s.ListMessages(q, ListOptions{Conversations: true})
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]string, len(p.Messages))
				for i, m := range p.Messages {
					ids[i] = m.ID
				}
				sums, err := s.Summaries(ids)
				if err != nil {
					t.Fatal(err)
				}
				byID := map[string]models.MessageSummary{}
				for _, m := range sums {
					byID[m.ID] = m
				}
				want := make([]models.MessageSummary, len(ids))
				for i, id := range ids {
					want[i] = byID[id]
				}
				if err := aggregateThreadsPerRow(s, want); err != nil {
					t.Fatal(err)
				}
				for i := range want {
					if !reflect.DeepEqual(p.Messages[i], want[i]) {
						t.Fatalf("hide=%v %+v page %d row %d:\n got %+v\nwant %+v", hide, q, pages, i, p.Messages[i], want[i])
					}
				}
				pages++
				if p.NextCursor == "" {
					break
				}
				q.Cursor = p.NextCursor
			}
			if pages < 2 {
				t.Fatalf("%+v: only %d page", q, pages)
			}
		}
	}
}
