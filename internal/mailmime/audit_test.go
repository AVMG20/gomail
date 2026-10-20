package mailmime

import (
	"strings"
	"testing"
)

func TestListUnsubscribeURLWithComma(t *testing.T) {
	u := ParseListUnsubscribe(`<https://x.example/u?a=1,2&b=3>, <mailto:u@x.example>`, "List-Unsubscribe=One-Click")
	if u.URL != "https://x.example/u?a=1,2&b=3" || !u.OneClick || u.Mailto != "mailto:u@x.example" {
		t.Fatalf("%+v", u)
	}
}

func TestProtocolRelativeRemoteContent(t *testing.T) {
	html := SanitizeHTML(`<p>hi</p><img src="//tracker.example/p.gif" width="1" height="1">`)
	out, blocked := BlockRemoteContent(html)
	if !HasRemoteContent(html) || !blocked || strings.Contains(out, "tracker.example") {
		t.Fatalf("not blocked: %q", out)
	}
	if HasRemoteContent(`<img src="images/local.png">`) {
		t.Fatal("relative path counted as remote")
	}
}

func TestMessageIDs(t *testing.T) {
	if got := MessageIDs(`<a@x> (comment) <b@y>`); got != "<a@x> <b@y>" {
		t.Fatal(got)
	}
}
