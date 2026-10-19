package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AVMG20/gomail/internal/models"
)

func TestMaintenanceAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "g.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var av int
	s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&av)
	if av != 2 {
		t.Fatalf("auto_vacuum = %d, want incremental", av)
	}
	if err := s.QuickCheck(); err != nil {
		t.Fatal(err)
	}
	if err := s.Maintain(); err != nil {
		t.Fatal(err)
	}
	s.InsertAccount(models.Account{ID: "a1", Provider: "gmail", Email: "me@x.com", DisplayName: "Me"})
	s.UpdateSignature("a1", "<p>sig</p>")
	s.SetSetting("settings", map[string]any{"theme": "dark"})
	s.Close()

	// Recovery keeps accounts and settings and moves the old file aside.
	if err := Recover(path); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Account("a1")
	var st map[string]any
	s.GetSetting("settings", &st)
	if err != nil || a.Signature != "<p>sig</p>" || st["theme"] != "dark" {
		t.Fatalf("salvage: %+v %v %v", a, st, err)
	}
	s.Close()
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) == 0 {
		t.Fatal("no backup of the old database")
	}

	// A file that isn't a database is reported as corrupt and recoverable.
	bad := filepath.Join(dir, "bad.db")
	os.WriteFile(bad, []byte("this is definitely not an sqlite database file, just text ......................................"), 0o600)
	if _, err := Open(bad); !IsCorrupt(err) {
		t.Fatalf("expected corruption error, got %v", err)
	}
	if err := Recover(bad); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(bad); err != nil {
		t.Fatal(err)
	} else {
		s.Close()
	}
}
