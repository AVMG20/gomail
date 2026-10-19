// Package mailmime builds outgoing RFC 5322 messages, parses incoming MIME,
// decodes charsets and sanitizes HTML bodies for display.
package mailmime

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// Attachment is an outgoing attachment.
type Attachment struct {
	Filename  string
	MimeType  string
	Data      []byte
	ContentID string // set for inline (cid:) parts
}

// Message is an outgoing message.
type Message struct {
	From        models.Address
	To, Cc, Bcc []models.Address
	ReplyTo     []models.Address
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
	InReplyTo   string
	References  string
	MessageID   string // generated when empty
	Date        time.Time
	// IncludeBcc writes the Bcc header (Gmail's API reads recipients from
	// headers and strips Bcc before delivery).
	IncludeBcc bool
	// Headers are extra header fields (values are written as given).
	Headers map[string]string
}

// FormatAddress encodes an address for a header, RFC 2047-encoding
// non-ASCII display names.
func FormatAddress(a models.Address) string {
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// FormatAddressList joins addresses for a header.
func FormatAddressList(list []models.Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if strings.TrimSpace(a.Email) == "" {
			continue
		}
		parts = append(parts, FormatAddress(a))
	}
	return strings.Join(parts, ", ")
}

// EncodeHeader RFC 2047-encodes a header value when it is not plain ASCII.
func EncodeHeader(s string) string {
	for _, r := range s {
		if r >= 0x80 || r < 0x20 && r != '\t' {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewMessageID returns a fresh Message-ID for the sender's domain.
func NewMessageID(from string) string {
	domain := "gomail.local"
	if i := strings.LastIndex(from, "@"); i >= 0 && i < len(from)-1 {
		domain = from[i+1:]
	}
	return "<" + randomToken(12) + "@" + domain + ">"
}

// Build renders the message as RFC 5322 bytes with CRLF line endings.
// Structure: multipart/mixed { multipart/related { multipart/alternative
// { text, html }, inline parts }, attachments }, collapsing levels that
// aren't needed.
func Build(m Message) ([]byte, error) {
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	if m.MessageID == "" {
		m.MessageID = NewMessageID(m.From.Email)
	}
	if m.Text == "" && m.HTML != "" {
		m.Text = HTMLToText(m.HTML)
	}
	var buf bytes.Buffer
	h := func(k, v string) {
		if v != "" {
			buf.WriteString(k + ": " + v + "\r\n")
		}
	}
	h("From", FormatAddress(m.From))
	h("To", FormatAddressList(m.To))
	h("Cc", FormatAddressList(m.Cc))
	if m.IncludeBcc {
		h("Bcc", FormatAddressList(m.Bcc))
	}
	h("Reply-To", FormatAddressList(m.ReplyTo))
	h("Subject", EncodeHeader(m.Subject))
	h("Date", m.Date.Format(time.RFC1123Z))
	h("Message-ID", m.MessageID)
	h("In-Reply-To", m.InReplyTo)
	h("References", m.References)
	extra := make([]string, 0, len(m.Headers))
	for k := range m.Headers {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		if !strings.ContainsAny(k, ":\r\n") && !strings.ContainsAny(m.Headers[k], "\r\n") {
			h(k, m.Headers[k])
		}
	}
	h("MIME-Version", "1.0")
	h("X-Mailer", "gomail")

	var inline, attached []Attachment
	for _, a := range m.Attachments {
		if a.ContentID != "" {
			inline = append(inline, a)
		} else {
			attached = append(attached, a)
		}
	}

	// Body part writers return (headers, body) so they can be nested.
	alt := func() (textproto.MIMEHeader, []byte, error) {
		if m.HTML == "" {
			return textPart("text/plain", m.Text)
		}
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for _, p := range []struct{ ct, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
			ph, body, err := textPart(p.ct, p.body)
			if err != nil {
				return nil, nil, err
			}
			pw, err := w.CreatePart(ph)
			if err != nil {
				return nil, nil, err
			}
			pw.Write(body)
		}
		w.Close()
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Type", "multipart/alternative; boundary=\""+w.Boundary()+"\"")
		return hdr, b.Bytes(), nil
	}
	related := func() (textproto.MIMEHeader, []byte, error) {
		ah, ab, err := alt()
		if err != nil || len(inline) == 0 {
			return ah, ab, err
		}
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		pw, err := w.CreatePart(ah)
		if err != nil {
			return nil, nil, err
		}
		pw.Write(ab)
		for _, a := range inline {
			if err := writeAttachment(w, a, true); err != nil {
				return nil, nil, err
			}
		}
		w.Close()
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Type", "multipart/related; boundary=\""+w.Boundary()+"\"")
		return hdr, b.Bytes(), nil
	}
	top := func() (textproto.MIMEHeader, []byte, error) {
		rh, rb, err := related()
		if err != nil || len(attached) == 0 {
			return rh, rb, err
		}
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		pw, err := w.CreatePart(rh)
		if err != nil {
			return nil, nil, err
		}
		pw.Write(rb)
		for _, a := range attached {
			if err := writeAttachment(w, a, false); err != nil {
				return nil, nil, err
			}
		}
		w.Close()
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Type", "multipart/mixed; boundary=\""+w.Boundary()+"\"")
		return hdr, b.Bytes(), nil
	}
	th, tb, err := top()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(th))
	for k := range th {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range th[k] {
			h(k, v)
		}
	}
	buf.WriteString("\r\n")
	buf.Write(tb)
	return buf.Bytes(), nil
}

func textPart(ct, body string) (textproto.MIMEHeader, []byte, error) {
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Type", ct+"; charset=utf-8")
	hdr.Set("Content-Transfer-Encoding", "quoted-printable")
	var b bytes.Buffer
	qw := quotedprintable.NewWriter(&b)
	if _, err := qw.Write([]byte(normalizeNewlines(body))); err != nil {
		return nil, nil, err
	}
	if err := qw.Close(); err != nil {
		return nil, nil, err
	}
	return hdr, b.Bytes(), nil
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func writeAttachment(w *multipart.Writer, a Attachment, inline bool) error {
	ct := a.MimeType
	if ct == "" {
		ct = mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Filename)))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	name := a.Filename
	if name == "" {
		name = "attachment"
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Type", mime.FormatMediaType(ct, map[string]string{"name": name}))
	disp := "attachment"
	if inline {
		disp = "inline"
		hdr.Set("Content-ID", "<"+strings.Trim(a.ContentID, "<>")+">")
	}
	hdr.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	hdr.Set("Content-Transfer-Encoding", "base64")
	pw, err := w.CreatePart(hdr)
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(a.Data)
	for len(enc) > 76 {
		fmt.Fprintf(pw, "%s\r\n", enc[:76])
		enc = enc[76:]
	}
	_, err = fmt.Fprintf(pw, "%s\r\n", enc)
	return err
}

// ReplySubject prefixes "Re: " unless already present.
func ReplySubject(s string) string {
	t := strings.TrimSpace(s)
	l := strings.ToLower(t)
	if strings.HasPrefix(l, "re:") || strings.HasPrefix(l, "aw:") || strings.HasPrefix(l, "sv:") {
		return t
	}
	return "Re: " + t
}

// ForwardSubject prefixes "Fwd: " unless already present.
func ForwardSubject(s string) string {
	t := strings.TrimSpace(s)
	l := strings.ToLower(t)
	if strings.HasPrefix(l, "fwd:") || strings.HasPrefix(l, "fw:") {
		return t
	}
	return "Fwd: " + t
}

// ReferencesFor computes the References header for a reply.
func ReferencesFor(parentRefs, parentMessageID string) string {
	refs := strings.Fields(parentRefs)
	if parentMessageID != "" {
		found := false
		for _, r := range refs {
			if r == parentMessageID {
				found = true
			}
		}
		if !found {
			refs = append(refs, parentMessageID)
		}
	}
	// Keep the header reasonably short (RFC 5322 suggests trimming).
	if len(refs) > 20 {
		refs = append(refs[:1], refs[len(refs)-19:]...)
	}
	return strings.Join(refs, " ")
}
