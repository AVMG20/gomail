package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

// Clearing the inbox must not pull in mail past the sync depth: once the
// cached mail reaches below the cutoff, older mail is offered, not fetched.
func TestHasMoreRemoteRespectsSyncDepth(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1") // inbox server total 10
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{msg(1, "x", "A", "INBOX", "ALL")}}, nil)
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	oldest, _ := s.OldestInFolder(inbox.ID)
	q := models.ListQuery{FolderID: inbox.ID}

	p, _ := s.ListMessages(q, ListOptions{OlderCutoff: oldest.Add(-time.Hour)})
	if !p.HasMoreRemote || p.OlderBeyondDepth {
		t.Fatalf("within the sync depth: %+v", p)
	}
	p, _ = s.ListMessages(q, ListOptions{OlderCutoff: oldest.Add(time.Hour)})
	if p.HasMoreRemote || !p.OlderBeyondDepth {
		t.Fatalf("past the sync depth: %+v", p)
	}
	s.SetOlderDone("a1", inbox.ID, true)
	p, _ = s.ListMessages(q, ListOptions{OlderCutoff: oldest.Add(time.Hour)})
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
