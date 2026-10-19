package mail

import (
	"testing"
	"time"
)

func TestEmptiness(t *testing.T) {
	if !(Unsubscribe{}).Empty() || (Unsubscribe{Mailto: "mailto:x@y"}).Empty() || (Unsubscribe{URL: "https://x"}).Empty() {
		t.Fatal("Unsubscribe.Empty")
	}
	yes := true
	for i, q := range []SearchQuery{
		{Terms: []string{"x"}}, {From: []string{"a"}}, {To: []string{"b"}}, {Subject: []string{"s"}},
		{Unread: &yes}, {Starred: &yes}, {HasAttachment: true}, {Before: time.Now()}, {After: time.Now()},
	} {
		if q.Empty() {
			t.Errorf("query %d reported empty", i)
		}
	}
	if !(SearchQuery{}).Empty() {
		t.Fatal("zero query not empty")
	}
}
