package secrets

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

// A value written to the fallback file while the keyring was unavailable
// wins over the stale keyring copy once the keyring works again.
func TestFallbackCopyWinsOverStaleKeyring(t *testing.T) {
	keyring.MockInit()
	k := keyringStore{service: "gomail-audit"}
	if err := k.Set("tok", "stale"); err != nil {
		t.Fatal(err)
	}
	a := &autoStore{kr: k, file: mustFile(t)}
	if err := a.file.Set("tok", "rotated"); err != nil { // written while disabled
		t.Fatal(err)
	}
	if v, err := a.Get("tok"); err != nil || v != "rotated" {
		t.Fatalf("got %q %v", v, err)
	}
	if v, _ := k.Get("tok"); v != "rotated" {
		t.Fatalf("not moved back into the keyring: %q", v)
	}
	if _, err := a.file.Get("tok"); !errors.Is(err, ErrNotFound) {
		t.Fatal("fallback copy kept after moving it")
	}
}
