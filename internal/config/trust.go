package config

import (
	"regexp"
	"slices"
	"strings"

	"github.com/AVMG20/gomail/internal/models"
)

// maxTrusted caps the trusted link domain and image sender lists.
const maxTrusted = 5000

var port = regexp.MustCompile(`:\d+$`)

var hostName = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeDomain turns "https://www.Example.com/x", "*.example.com" or
// "example.com." into "example.com". It returns "" for anything that is
// not a host name.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndexByte(d, '@'); i >= 0 {
		d = d[i+1:]
	}
	d = port.ReplaceAllString(d, "")
	d = strings.TrimSuffix(strings.TrimPrefix(d, "*."), ".")
	d = strings.TrimPrefix(d, "www.")
	if len(d) > 253 || !hostName.MatchString(d) {
		return ""
	}
	return d
}

// NormalizeDomains cleans the trusted link domains: valid lowercase host
// names without duplicates (first occurrence wins). Never nil.
func NormalizeDomains(in []string) []string { return normalizeList(in, NormalizeDomain) }

// NormalizeSender lowercases an email address; "" when it isn't one.
func NormalizeSender(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	at := strings.LastIndexByte(e, '@')
	if at <= 0 || at == len(e)-1 || len(e) > 320 || strings.ContainsAny(e, " \t\r\n<>,;\"") {
		return ""
	}
	return e
}

// NormalizeSenders cleans the trusted image senders: valid lowercase
// addresses without duplicates (first occurrence wins). Never nil.
func NormalizeSenders(in []string) []string { return normalizeList(in, NormalizeSender) }

func normalizeList(in []string, norm func(string) string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		if v = norm(v); v != "" && !seen[v] && len(out) < maxTrusted {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// RemoteImagesAllowed reports whether mail from sender loads remote
// images: the global switch is on or the sender is trusted.
func RemoteImagesAllowed(s *models.Settings, sender string) bool {
	if s.ShowRemoteImages {
		return true
	}
	e := NormalizeSender(sender)
	return e != "" && slices.Contains(s.TrustedImageSenders, e)
}
