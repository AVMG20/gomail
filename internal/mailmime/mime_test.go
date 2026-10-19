package mailmime

import (
	"bytes"
	"strings"
	"testing"
	"time"

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

// TestBuildUsesCRLF: every line break of a built message is CRLF, whatever
// the platform or the line endings of the input (Windows text, Unix text,
// old Mac text, long lines, binary attachments).
func TestBuildUsesCRLF(t *testing.T) {
	raw, err := Build(Message{
		From: models.Address{Name: "Zoë Example", Email: "zoe@example.com"}, To: []models.Address{{Email: "a@example.com"}},
		Subject: "Line endings ✓ " + strings.Repeat("long subject ", 10),
		Text:    "unix\nwindows\r\nold mac\rend " + strings.Repeat("x", 200),
		HTML:    "<p>unix</p>\n<p>windows</p>\r\n" + strings.Repeat("<b>y</b>", 100),
		Attachments: []Attachment{{Filename: "notes.txt", MimeType: "text/plain", Data: []byte("a\nb\nc")},
			{Filename: "bin.dat", MimeType: "application/octet-stream", Data: bytes.Repeat([]byte{0, '\n', 0xff, '\r'}, 300)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range raw {
		if b == '\n' && (i == 0 || raw[i-1] != '\r') {
			t.Fatalf("bare LF at byte %d: %q", i, raw[max(0, i-40):i+1])
		}
		if b == '\r' && (i+1 == len(raw) || raw[i+1] != '\n') {
			t.Fatalf("bare CR at byte %d: %q", i, raw[max(0, i-40):i+1])
		}
	}
	for _, line := range bytes.Split(raw, []byte("\r\n")) {
		if len(line) > 998 {
			t.Fatalf("line longer than 998 octets: %d", len(line))
		}
	}
	m, err := Parse(raw)
	if err != nil || !strings.Contains(m.Text, "old mac") || len(m.Attachments) != 2 {
		t.Fatalf("parse back: %v %+v", err, m)
	}
	if got := m.Attachments[1].Data; !bytes.Equal(got, bytes.Repeat([]byte{0, '\n', 0xff, '\r'}, 300)) {
		t.Fatal("binary attachment changed")
	}
}

// TestSanitizeBypasses collects known sanitizer bypass attempts.
func TestSanitizeBypasses(t *testing.T) {
	png := "data:image/png;base64,iVBORw0KGgo="
	cases := []struct {
		in       string
		mustNot  []string
		mustHave []string
	}{
		{`<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`, []string{"data:text/html", "href"}, []string{"x"}},
		{`<a href="javascript:alert(1)">x</a><a href="vbscript:x">y</a>`, []string{"javascript:", "vbscript:"}, nil},
		{`<img src="` + png + `">`, nil, []string{png}},
		{`<img src="data:text/html,<script>alert(1)</script>">`, []string{"data:text/html", "<script"}, nil},
		{`<svg onload="alert(1)"><script>alert(1)</script></svg>`, []string{"<svg", "onload", "<script"}, nil},
		{`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`, []string{"<img", "<math", "<mglyph"}, nil}, // the rest is inert CSS text
		{`<base href="https://evil.example/"><meta http-equiv="refresh" content="0;url=https://evil.example">`, []string{"<base", "<meta", "refresh"}, nil},
		{`<form action="https://evil.example"><input name="password"><button formaction="https://e">go</button></form>`, []string{"<form", "<input", "formaction", "action="}, nil},
		{`<iframe src="https://evil.example"></iframe><object data="x"></object><embed src="x">`, []string{"<iframe", "<object", "<embed"}, nil},
		{`<style>@import url(https://t.example/a.css); p{color:red}</style >`, []string{"@import"}, []string{"color:red"}},
		{`<style>@\69mport "https://t.example/a.css"; b{x:expr\65ssion(alert(1))}</style>`, []string{"@import", "expression(", `\69`}, nil},
		{`<style>p{color:red}`, nil, []string{"color:red"}},
		{`<p style="background:url(javascript:alert(1))">x</p>`, []string{"javascript"}, nil},
		{`<p style="width:expr\65ssion(alert(1))">x</p>`, []string{"expr"}, nil},
		{`<div onclick="alert(1)" onmouseover=x>y</div>`, []string{"onclick", "onmouseover"}, []string{">y</div>"}},
	}
	for _, c := range cases {
		out := SanitizeHTML(c.in)
		for _, bad := range c.mustNot {
			if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
				t.Errorf("SanitizeHTML(%q) = %q, contains %q", c.in, out, bad)
			}
		}
		for _, good := range c.mustHave {
			if !strings.Contains(out, good) {
				t.Errorf("SanitizeHTML(%q) = %q, lost %q", c.in, out, good)
			}
		}
	}
	// Remote loads hidden in image-set() are blocked too.
	in := SanitizeHTML(`<p style="background-image:image-set(&#34;https://t.example/p.png&#34; 1x)">x</p><style>b{background:-webkit-image-set("https://t.example/q.png" 1x)}</style>`)
	out, blocked := BlockRemoteContent(in)
	if !blocked || strings.Contains(out, "t.example") {
		t.Fatalf("image-set not blocked: %q", out)
	}
}

func TestBuildHeaderInjection(t *testing.T) {
	raw, err := Build(Message{
		From:       models.Address{Name: "Eve\r\nBcc: victim@example.com", Email: "eve@example.com"},
		To:         []models.Address{{Name: "x", Email: "a@example.com\r\nBcc: v2@example.com"}},
		Subject:    "Hi\r\nBcc: v3@example.com",
		InReplyTo:  "<id@x>\r\nBcc: v4@example.com",
		References: "<a@x>\nX-Evil: 1",
		Text:       "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") || strings.HasPrefix(line, "X-Evil") {
			t.Fatalf("header injected: %q\n%s", line, head)
		}
	}
}

func TestParseDateInTheWild(t *testing.T) {
	want := time.Date(2006, 1, 2, 20, 4, 5, 0, time.UTC)
	for _, s := range []string{
		"Mon, 2 Jan 2006 15:04:05 -0500", "Mon,  2 Jan 2006 15:04:05 -0500 (EST)", "Mon, 02 Jan 06 15:04:05 EST",
		"Monday, 2 January 2006 15:04:05 -0500", "2006-01-02T15:04:05-05:00", "Mon, 2 Jan 2006 20:04:05",
		"Mon, 2 Jan 2006 20:04:05 GMT", "Mon, 2 Jan 2006 12:04:05 PST", "Mon Jan 2 20:04:05 2006",
	} {
		got := ParseDate(s)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseDate(%q) = %v, want %v", s, got, want)
		}
	}
	if !ParseDate("not a date").IsZero() {
		t.Fatal("garbage parsed")
	}
}
