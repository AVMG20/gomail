package mailto

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	c, ok := Parse("mailto:alice@example.com,Bob%20B%20%3Cbob@example.com%3E?subject=Hi%20there%20%26%20more&cc=carol@x.com&bcc=dan@x.com&body=Line%201%0D%0ALine%202&x-ignored=1")
	if !ok || len(c.To) != 2 || c.To[1].Name != "Bob B" || c.To[1].Email != "bob@example.com" || c.Subject != "Hi there & more" ||
		len(c.Cc) != 1 || len(c.Bcc) != 1 || c.Body != "Line 1\nLine 2" {
		t.Fatalf("%+v", c)
	}
	c, _ = Parse("MAILTO:?to=a@b.c&subject=a+b")
	if len(c.To) != 1 || c.Subject != "a+b" {
		t.Fatalf("%+v", c)
	}
	if _, ok := Parse("https://x"); ok {
		t.Fatal("not mailto")
	}
	if c, ok := FromArgs([]string{"--minimized", "mailto:x@y.z"}); !ok || c.To[0].Email != "x@y.z" {
		t.Fatal(c)
	}
	if c, _ := Parse("mailto:not-an-address"); len(c.To) != 0 {
		t.Fatal(c)
	}
}

func TestParseLimits(t *testing.T) {
	if _, ok := Parse("mailto:a@b.c?body=" + strings.Repeat("x", maxURI)); ok {
		t.Fatal("oversized link accepted")
	}
	c, _ := Parse("mailto:a@b.c?subject=Hi%0D%0ABcc:%20x@y.z")
	if strings.ContainsAny(c.Subject, "\r\n") {
		t.Fatalf("subject %q", c.Subject)
	}
	c, _ = Parse("mailto:" + strings.Repeat("x@y.z,", 150))
	if len(c.To) != maxRecipients {
		t.Fatalf("%d recipients", len(c.To))
	}
}
