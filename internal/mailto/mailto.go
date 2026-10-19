// Package mailto parses RFC 6068 mailto: URIs.
package mailto

import (
	"net/url"
	"strings"

	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
)

// Limits for links from the command line or other apps.
const (
	maxURI        = 256 << 10
	maxRecipients = 100
)

// IsMailto reports whether s looks like a mailto: URI.
func IsMailto(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "mailto:")
}

// Parse turns a mailto: URI into composer fields. Headers other than to,
// cc, bcc, subject and body are ignored (RFC 6068 lets clients do so).
func Parse(raw string) (models.ComposeOpen, bool) {
	out := models.ComposeOpen{To: []models.Address{}, Cc: []models.Address{}, Bcc: []models.Address{}}
	raw = strings.TrimSpace(raw)
	if !IsMailto(raw) || len(raw) > maxURI {
		return out, false
	}
	rest := raw[len("mailto:"):]
	addrPart, query, _ := strings.Cut(rest, "?")
	add := func(list *[]models.Address, v string) {
		v, err := url.PathUnescape(v)
		if err != nil {
			return
		}
		for _, a := range mailmime.ParseAddressList(v) {
			if strings.Contains(a.Email, "@") && len(*list) < maxRecipients {
				*list = append(*list, a)
			}
		}
	}
	add(&out.To, addrPart)
	q, _ := url.ParseQuery(strings.ReplaceAll(query, "+", "%2B")) // '+' is literal in mailto
	for k, vals := range q {
		for _, v := range vals {
			switch strings.ToLower(k) {
			case "to":
				add(&out.To, url.PathEscape(v))
			case "cc":
				add(&out.Cc, url.PathEscape(v))
			case "bcc":
				add(&out.Bcc, url.PathEscape(v))
			case "subject":
				out.Subject = strings.Join(strings.Fields(v), " ") // one line
			case "body":
				out.Body = strings.ReplaceAll(v, "\r\n", "\n")
			}
		}
	}
	return out, true
}

// FromArgs finds the first mailto: argument.
func FromArgs(args []string) (models.ComposeOpen, bool) {
	for _, a := range args {
		if IsMailto(a) {
			return Parse(a)
		}
	}
	return models.ComposeOpen{}, false
}
