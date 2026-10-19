package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	big := strings.Repeat("x", 10000) // larger than the Windows credential limit
	if err := f.Set("a", "secret-value"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("b", big); err != nil {
		t.Fatal(err)
	}
	// A new instance reads the same data.
	g, _ := NewFileStore(dir)
	if v, err := g.Get("a"); err != nil || v != "secret-value" {
		t.Fatal(v, err)
	}
	if v, _ := g.Get("b"); v != big {
		t.Fatal("big value")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("stored in plain text")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "secrets.enc"))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	if err := g.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("not deleted")
	}
	// A replaced key makes the store undecryptable with a clear error.
	os.WriteFile(filepath.Join(dir, "secrets.key"), make([]byte, 32), 0o600)
	if _, err := f.Get("b"); err == nil || !strings.Contains(err.Error(), "decrypted") {
		t.Fatalf("tampered key: %v", err)
	}
}

func TestForcedFileBackend(t *testing.T) {
	t.Setenv(EnvBackend, "file")
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*FileStore); !ok {
		t.Fatalf("got %T", s)
	}
}
