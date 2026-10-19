package mailmime

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/AVMG20/gomail/internal/models"
)

// Part is a decoded leaf part of a parsed message.
type Part struct {
	ID          string // MIME part path, e.g. "1.2"
	ContentType string
	Filename    string
	ContentID   string
	Inline      bool
	Data        []byte // transfer-decoded bytes
}

// Parsed is a parsed message.
type Parsed struct {
	From        models.Address
	To, Cc, Bcc []models.Address
	ReplyTo     []models.Address
	Subject     string
	Date        time.Time
	MessageID   string
	InReplyTo   string
	References  string
	HTML        string // UTF-8
	Text        string // UTF-8
	Attachments []Part // non-body parts (including inline images)
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(cs string, r io.Reader) (io.Reader, error) {
	return charset.NewReaderLabel(cs, r)
}}

// DecodeHeader decodes RFC 2047 encoded words.
func DecodeHeader(s string) string {
	d, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return d
}

// ParseAddressList parses an address header leniently.
func ParseAddressList(s string) []models.Address {
	out := []models.Address{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	p := mail.AddressParser{WordDecoder: wordDecoder}
	list, err := p.ParseList(s)
	if err == nil {
		for _, a := range list {
			out = append(out, models.Address{Name: a.Name, Email: a.Address})
		}
		return out
	}
	// Fallback: split on commas and parse each, keeping what we can.
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, err := p.Parse(part); err == nil {
			out = append(out, models.Address{Name: a.Name, Email: a.Address})
		} else if strings.Contains(part, "@") {
			out = append(out, models.Address{Email: strings.Trim(part, "<> \"")})
		}
	}
	return out
}

// ParseAddress parses a single address header.
func ParseAddress(s string) models.Address {
	l := ParseAddressList(s)
	if len(l) == 0 {
		return models.Address{Name: DecodeHeader(s)}
	}
	return l[0]
}

// ParseDate parses a Date header leniently.
func ParseDate(s string) time.Time {
	if t, err := mail.ParseDate(s); err == nil {
		return t
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700 (MST)", "2 Jan 2006 15:04:05 -0700"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

// DecodeCharset converts data in the named charset to UTF-8. Unknown
// charsets fall back to UTF-8 (if valid) or Windows-1252.
func DecodeCharset(data []byte, cs string) string {
	cs = strings.ToLower(strings.TrimSpace(cs))
	if cs == "" || cs == "utf-8" || cs == "utf8" || cs == "us-ascii" {
		if utf8.Valid(data) {
			return string(data)
		}
		if cs == "" || cs == "us-ascii" {
			cs = "windows-1252"
		}
	}
	r, err := charset.NewReaderLabel(cs, bytes.NewReader(data))
	if err != nil {
		if utf8.Valid(data) {
			return string(data)
		}
		r, _ = charset.NewReaderLabel("windows-1252", bytes.NewReader(data))
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return strings.ToValidUTF8(string(data), "�")
	}
	return string(out)
}

// DecodeTransfer decodes a Content-Transfer-Encoding.
func DecodeTransfer(data []byte, enc string) []byte {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		clean := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, data)
		out := make([]byte, base64.StdEncoding.DecodedLen(len(clean)))
		n, err := base64.StdEncoding.Decode(out, clean)
		if err != nil {
			// Try without padding, and keep whatever decoded.
			if n2, err2 := base64.RawStdEncoding.Decode(out, bytes.TrimRight(clean, "=")); err2 == nil {
				return out[:n2]
			}
		}
		return out[:n]
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err != nil && len(out) == 0 {
			return data
		}
		return out
	}
	return data
}

// Parse parses a raw RFC 5322 message.
func Parse(raw []byte) (*Parsed, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}
	h := msg.Header
	p := &Parsed{
		From:       ParseAddress(h.Get("From")),
		To:         ParseAddressList(h.Get("To")),
		Cc:         ParseAddressList(h.Get("Cc")),
		Bcc:        ParseAddressList(h.Get("Bcc")),
		ReplyTo:    ParseAddressList(h.Get("Reply-To")),
		Subject:    DecodeHeader(h.Get("Subject")),
		Date:       ParseDate(h.Get("Date")),
		MessageID:  strings.TrimSpace(h.Get("Message-ID")),
		InReplyTo:  strings.TrimSpace(h.Get("In-Reply-To")),
		References: strings.Join(strings.Fields(h.Get("References")), " "),
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, err
	}
	walk(p, "", h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), h.Get("Content-Disposition"), h.Get("Content-ID"), body, 0)
	return p, nil
}

func walk(p *Parsed, id, ctype, cte, cdisp, cid string, body []byte, depth int) {
	if depth > 20 {
		return
	}
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil || mt == "" {
		mt, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for i := 1; ; i++ {
			part, err := mr.NextRawPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			sub := fmt.Sprint(i)
			if id != "" {
				sub = id + "." + sub
			}
			walk(p, sub, part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"),
				part.Header.Get("Content-Disposition"), part.Header.Get("Content-ID"), data, depth+1)
		}
		return
	}
	if id == "" {
		id = "1"
	}
	data := DecodeTransfer(body, cte)
	disp, dparams, _ := mime.ParseMediaType(cdisp)
	filename := DecodeHeader(dparams["filename"])
	if filename == "" {
		filename = DecodeHeader(params["name"])
	}
	isAttachment := strings.EqualFold(disp, "attachment") || (filename != "" && !strings.HasPrefix(mt, "text/"))
	if mt == "message/rfc822" {
		isAttachment = true
		if filename == "" {
			filename = "message.eml"
		}
	}
	if !isAttachment && (mt == "text/html" || mt == "text/plain") {
		text := DecodeCharset(data, params["charset"])
		if mt == "text/html" {
			if p.HTML == "" {
				p.HTML = text
			} else {
				p.HTML += "<hr>" + text
			}
		} else if p.Text == "" {
			p.Text = text
		} else {
			p.Text += "\n\n" + text
		}
		return
	}
	p.Attachments = append(p.Attachments, Part{
		ID:          id,
		ContentType: mt,
		Filename:    filename,
		ContentID:   strings.Trim(strings.TrimSpace(cid), "<>"),
		Inline:      strings.EqualFold(disp, "inline") || (disp == "" && cid != ""),
		Data:        data,
	})
}
