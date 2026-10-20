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

// Browsers load "https:\\host/x" and "https:host/x" from host just like
// "https://host/x", so they are remote content too.
func TestSlashlessRemoteContentIsBlocked(t *testing.T) {
	for _, in := range []string{
		`<img src="https:\\tracker.example/p.gif">`,
		`<img src="https:tracker.example/p.gif">`,
		`<img src="HTTP:/tracker.example/p.gif">`,
		`<table background="https:\\tracker.example/bg.gif"><tr><td>x</td></tr></table>`,
		`<div style="background-image:url(https:tracker.example/p.gif)">x</div>`,
		`<style>p{background:url('http:tracker.example/a.gif')}</style><p>x</p>`,
	} {
		html := SanitizeHTML(in)
		out, blocked := BlockRemoteContent(html)
		if !HasRemoteContent(html) || !blocked || strings.Contains(out, "tracker.example") {
			t.Errorf("%s: not blocked: %q", in, out)
		}
	}
	if HasRemoteContent(`<img src="https-logo.png"><div style="background:url(img/http.png)">x</div>`) {
		t.Fatal("relative paths counted as remote")
	}
}

func TestMessageIDs(t *testing.T) {
	if got := MessageIDs(`<a@x> (comment) <b@y>`); got != "<a@x> <b@y>" {
		t.Fatal(got)
	}
}
