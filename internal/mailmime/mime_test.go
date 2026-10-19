package mailmime

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AVMG20/gomail/internal/models"
)

func TestBuildAndParseRoundTrip(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfakeimage")
	pdf := bytes.Repeat([]byte("%PDF-1.4 binary \x00\xff"), 50)
	raw, err := Build(Message{
		From:        models.Address{Name: "Jürgen Müller", Email: "juergen@example.de"},
		To:          []models.Address{{Name: "Zoë", Email: "zoe@example.com"}, {Email: "bob@example.com"}},
		Cc:          []models.Address{{Name: "Team, Inc.", Email: "team@example.com"}},
		Bcc:         []models.Address{{Email: "secret@example.com"}},
		IncludeBcc:  true,
		Subject:     "Grüße aus München — 日本語",
		Text:        "Hallo Zoë,\nwie geht's?\n",
		HTML:        `<p>Hallo <b>Zoë</b></p><img src="cid:logo@x">`,
		InReplyTo:   "<parent@example.com>",
		References:  "<root@example.com> <parent@example.com>",
		Attachments: []Attachment{{Filename: "Bericht über.pdf", MimeType: "application/pdf", Data: pdf}, {Filename: "logo.png", MimeType: "image/png", Data: png, ContentID: "logo@x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"multipart/mixed", "multipart/related", "multipart/alternative", "Subject: =?utf-8?q?", "In-Reply-To: <parent@example.com>", "Message-ID: <"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, line := range strings.Split(s, "\r\n") {
		if len(line) > 998 {
			t.Fatal("line too long")
		}
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "Grüße aus München — 日本語" {
		t.Errorf("subject %q", p.Subject)
	}
	if p.From.Name != "Jürgen Müller" || p.From.Email != "juergen@example.de" {
		t.Errorf("from %+v", p.From)
	}
	if len(p.To) != 2 || p.To[0].Name != "Zoë" || len(p.Cc) != 1 || p.Cc[0].Name != "Team, Inc." || len(p.Bcc) != 1 {
		t.Errorf("recipients %+v %+v %+v", p.To, p.Cc, p.Bcc)
	}
	if !strings.Contains(p.Text, "wie geht's?") || !strings.Contains(p.HTML, "<b>Zoë</b>") {
		t.Errorf("bodies %q %q", p.Text, p.HTML)
	}
	if p.References != "<root@example.com> <parent@example.com>" {
		t.Errorf("refs %q", p.References)
	}
	if len(p.Attachments) != 2 {
		t.Fatalf("attachments %d", len(p.Attachments))
	}
	var gotPDF, gotPNG *Part
	for i := range p.Attachments {
		switch p.Attachments[i].ContentType {
		case "application/pdf":
			gotPDF = &p.Attachments[i]
		case "image/png":
			gotPNG = &p.Attachments[i]
		}
	}
	if gotPDF == nil || gotPDF.Filename != "Bericht über.pdf" || !bytes.Equal(gotPDF.Data, pdf) || gotPDF.Inline {
		t.Errorf("pdf %+v", gotPDF)
	}
	if gotPNG == nil || gotPNG.ContentID != "logo@x" || !gotPNG.Inline || !bytes.Equal(gotPNG.Data, png) {
		t.Errorf("png %+v", gotPNG)
	}
	html := RewriteCID(p.HTML, []InlineImage{{ContentID: gotPNG.ContentID, MimeType: gotPNG.ContentType, Data: gotPNG.Data}})
	if !strings.Contains(html, `src="data:image/png;base64,`) {
		t.Errorf("cid not rewritten: %s", html)
	}
	if clean := SanitizeHTML(html); !strings.Contains(clean, "data:image/png;base64,") {
		t.Errorf("sanitizer dropped data uri: %s", clean)
	}
}

func TestParseLegacyCharsets(t *testing.T) {
	raw := "From: =?iso-8859-1?q?Andr=E9?= <andre@example.fr>\r\n" +
		"To: x@example.com\r\n" +
		"Subject: =?windows-1252?Q?Caf=E9_=96_menu?=\r\n" +
		"Date: Tue, 6 Oct 2026 10:00:00 +0200\r\n" +
		"Content-Type: text/plain; charset=ISO-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Cr=E8me br=FBl=E9e\r\n"
	p, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.From.Name != "André" || p.Subject != "Café – menu" || strings.TrimSpace(p.Text) != "Crème brûlée" {
		t.Fatalf("%+v", p)
	}
	if p.Date.IsZero() {
		t.Fatal("date")
	}
	// Shift-JIS html, base64
	raw2 := "Content-Type: text/html; charset=shift_jis\r\nContent-Transfer-Encoding: base64\r\n\r\ngrGC8YLJgr+CzQ==\r\n"
	p, err = Parse([]byte(raw2))
	if err != nil || p.HTML != "こんにちは" {
		t.Fatalf("sjis %q %v", p.HTML, err)
	}
}

func TestSanitizeHTML(t *testing.T) {
	in := `<html><head><style>body{color:red} @import url(http://evil/x.css); .a{background:url(javascript:alert(1))}</style></head>
<body onload="alert(1)"><script>alert(1)</script><iframe src="http://x"></iframe>
<table width="100%" cellpadding="0" bgcolor="#fff"><tr><td style="padding: 10px; color: #333; position: fixed" onclick="x()">Hi</td></tr></table>
<a href="javascript:alert(1)">bad</a><a href="https://example.com">good</a><form action="x"><input name="p"></form>
<img src="https://example.com/i.png" onerror="alert(1)"><div style="width: expression(alert(1))">x</div></body></html>`
	out := SanitizeHTML(in)
	for _, bad := range []string{"<script", "alert(1)</script", "onload", "onclick", "onerror", "<iframe", "javascript:", "<form", "<input", "@import", "expression(", "position"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
			t.Errorf("sanitized output contains %q:\n%s", bad, out)
		}
	}
	for _, good := range []string{"<table", `width="100%"`, `bgcolor="#fff"`, "padding: 10px", `href="https://example.com"`, `<img src="https://example.com/i.png"`, "<style>", "color:red"} {
		if !strings.Contains(out, good) {
			t.Errorf("sanitized output lost %q:\n%s", good, out)
		}
	}
	if !HasRemoteContent(out) {
		t.Error("remote content not detected")
	}
	if HasRemoteContent(`<img src="data:image/png;base64,AAA">`) {
		t.Error("data uri is not remote")
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText(`<html><head><title>T</title><style>p{}</style></head><body><h1>Hello &amp; welcome</h1><p>Line one<br>Line two</p><ul><li>A</li><li>B</li></ul><a href="https://x.io">link</a></body></html>`)
	want := "Hello & welcome\nLine one\nLine two\n\n• A\n• B\nlink <https://x.io>"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestReplyHelpers(t *testing.T) {
	if ReplySubject("Re: x") != "Re: x" || ReplySubject("x") != "Re: x" || ForwardSubject("x") != "Fwd: x" {
		t.Fatal("subjects")
	}
	if got := ReferencesFor("<a> <b>", "<c>"); got != "<a> <b> <c>" {
		t.Fatal(got)
	}
	if got := ReferencesFor("", "<c>"); got != "<c>" {
		t.Fatal(got)
	}
}

func TestBlockRemoteContentAndLinks(t *testing.T) {
	in := `<a href="https://example.com/x">x</a><img src="https://t.example/p.gif" srcset="https://a/1.png 1x, https://a/2.png 2x" alt="a">` +
		`<img src="data:image/png;base64,AAAA"><img srcset="javascript:alert(1) 1x"><table background="http://bg/x.png"><tr><td style="background-image: url('https://bg/y.png'); color: red">t</td></tr></table>` +
		`<style>.x{background:url(https://bg/z.png)}</style>`
	clean := SanitizeHTML(in)
	if !strings.Contains(clean, `rel="noreferrer noopener"`) && !strings.Contains(clean, `noopener`) {
		t.Errorf("links lack noopener: %s", clean)
	}
	if strings.Contains(clean, "javascript") {
		t.Errorf("unsafe srcset kept: %s", clean)
	}
	if !strings.Contains(clean, `srcset="https://a/1.png 1x, https://a/2.png 2x"`) {
		t.Errorf("safe srcset dropped: %s", clean)
	}
	if !HasRemoteContent(clean) {
		t.Fatal("remote content not detected")
	}
	blocked, changed := BlockRemoteContent(clean)
	if !changed || HasRemoteContent(blocked) {
		t.Fatalf("still remote after blocking: %s", blocked)
	}
	for _, keep := range []string{`href="https://example.com/x"`, "data:image/png;base64,AAAA", "color: red", `alt="a"`} {
		if !strings.Contains(blocked, keep) {
			t.Errorf("blocking removed %q: %s", keep, blocked)
		}
	}
}

func TestParseListUnsubscribe(t *testing.T) {
	u := ParseListUnsubscribe("<mailto:unsub@news.example?subject=unsubscribe>, <https://news.example/u?id=1>", "List-Unsubscribe=One-Click")
	if u.Mailto != "mailto:unsub@news.example?subject=unsubscribe" || u.URL != "https://news.example/u?id=1" || !u.OneClick {
		t.Fatalf("%+v", u)
	}
	u = ParseListUnsubscribe("<http://x.example/u>, <javascript:alert(1)>", "List-Unsubscribe=One-Click")
	if u.URL != "http://x.example/u" || u.OneClick {
		t.Fatalf("http one-click must be refused: %+v", u)
	}
	if u := ParseListUnsubscribe("garbage", ""); !u.Empty() {
		t.Fatalf("%+v", u)
	}
	raw, _ := Build(Message{From: models.Address{Email: "a@b.c"}, Subject: "x", Text: "y",
		Headers: map[string]string{"List-Unsubscribe": "<https://n.example/u>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click", "Bad\nHeader": "x"}})
	p, _ := Parse(raw)
	if !p.Unsubscribe.OneClick || strings.Contains(string(raw), "Bad") {
		t.Fatalf("%+v", p.Unsubscribe)
	}
}
