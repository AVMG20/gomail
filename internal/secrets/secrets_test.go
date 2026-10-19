package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
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

func TestKeyringChunking(t *testing.T) {
	keyring.MockInit()
	old := chunkSize
	chunkSize = 10
	defer func() { chunkSize = old }()
	k := keyringStore{service: "gomail-test"}
	long := strings.Repeat("0123456789", 7) + "xyz" // 73 bytes -> 8 chunks
	if err := k.Set("tok", long); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Get("tok"); err != nil || v != long {
		t.Fatalf("%q %v", v, err)
	}
	if n := k.chunks("tok"); n != 8 {
		t.Fatalf("chunks %d", n)
	}
	// Shrinking back to a single entry removes the old chunks.
	if err := k.Set("tok", "short"); err != nil {
		t.Fatal(err)
	}
	if v, _ := k.Get("tok"); v != "short" {
		t.Fatal(v)
	}
	if _, err := k.raw(chunkKey("tok", 0)); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale chunk left behind")
	}
	k.Set("tok", long)
	if err := k.Delete("tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get("tok"); !errors.Is(err, ErrNotFound) {
		t.Fatal("not deleted")
	}
	if _, err := k.raw(chunkKey("tok", 3)); !errors.Is(err, ErrNotFound) {
		t.Fatal("chunk not deleted")
	}
	// autoStore uses the keyring when it works.
	a := &autoStore{kr: k, file: mustFile(t)}
	if err := a.Set("x", long); err != nil {
		t.Fatal(err)
	}
	if _, err := a.file.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("value also written to the fallback file")
	}
	if v, _ := a.Get("x"); v != long {
		t.Fatal(v)
	}
}

func mustFile(t *testing.T) *FileStore {
	f, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
