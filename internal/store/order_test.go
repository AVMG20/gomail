package store

import (
	"slices"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

func TestReorderAccounts(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	for i, id := range []string{"a", "b", "c"} {
		if err := s.InsertAccount(models.Account{ID: id, Provider: "gmail", Email: id + "@example.com", CreatedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	order := func() []string {
		accts, err := s.Accounts()
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, a := range accts {
			ids = append(ids, a.ID)
		}
		return ids
	}
	if got := order(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("initial order %v", got)
	}
	// Unknown ids are ignored, missing ones keep their order after the listed ones.
	if err := s.ReorderAccounts([]string{"c", "x", "a"}); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("after reorder %v", got)
	}
	// A new mailbox goes last, whatever its creation time.
	if err := s.InsertAccount(models.Account{ID: "d", Provider: "gmail", Email: "d@example.com", CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("after insert %v", got)
	}
}
