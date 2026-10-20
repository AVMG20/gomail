package core

import (
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// Deleting a custom folder removes it on the server and here; its mail goes
// to Trash. Built-in folders can't be deleted.
func TestDeleteFolder(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	acct := demo.OutlookAccountID
	e.demos[acct].Deliver("Keep me", "ana@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	folders, _ := s.Folders(acct)
	var projects, inbox models.Folder
	for _, f := range folders {
		switch {
		case f.Name == "Projects":
			projects = f
		case f.Kind == models.FolderInbox:
			inbox = f
		}
	}
	m, ok := labelsOf(t, e, acct, "Keep me")
	if !ok || projects.ID == "" {
		t.Fatal("setup")
	}
	if err := s.MoveToFolder([]string{m.ID}, projects.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(inbox.ID); err == nil {
		t.Fatal("deleted the inbox")
	}
	if err := s.DeleteFolder(projects.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	folders, _ = s.Folders(acct)
	for _, f := range folders {
		if f.ID == projects.ID {
			t.Fatal("folder still listed")
		}
	}
	trash, _ := s.store.FolderByKind(acct, models.FolderTrash)
	if m, ok := labelsOf(t, e, acct, "Keep me"); !ok || !has(m.FolderIDs, trash.ID) || len(m.FolderIDs) != 1 {
		t.Fatalf("mail of the deleted folder: %v (trash %s)", m.FolderIDs, trash.ID)
	}
}
