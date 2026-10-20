package config

import (
	"slices"
	"testing"

	"github.com/AVMG20/gomail/internal/models"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"example.com":                         "example.com",
		"  Example.COM ":                      "example.com",
		"www.example.com":                     "example.com",
		"https://www.Example.com/a?b=1#c":     "example.com",
		"http://user@shop.example.com:8080/x": "shop.example.com",
		"*.example.com":                       "example.com",
		"example.com.":                        "example.com",
		"xn--bcher-kva.example":               "xn--bcher-kva.example",
		"localhost":                           "localhost",
		"":                                    "",
		"   ":                                 "",
		"exa mple.com":                        "",
		"-bad.example.com":                    "",
		"javascript:alert(1)":                 "",
		"bad_host.example.com":                "",
	}
	for in, want := range cases {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSender(t *testing.T) {
	cases := map[string]string{
		"alex.morgan@example.com":     "alex.morgan@example.com",
		"  Alex.Morgan@Example.COM  ": "alex.morgan@example.com",
		"sam":                         "",
		"@example.com":                "",
		"sam@":                        "",
		"Sam <sam@example.com>":       "",
		"a b@example.com":             "",
		"":                            "",
	}
	for in, want := range cases {
		if got := NormalizeSender(in); got != want {
			t.Errorf("NormalizeSender(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTrustedLists(t *testing.T) {
	s := models.Settings{
		TrustedLinkDomains:  []string{"Example.com", "www.example.com", "", "not a domain", "news.example.org"},
		TrustedImageSenders: []string{"Sam@Example.com", "sam@example.com", "nope", "alex.morgan@example.com"},
	}
	Normalize(&s)
	if want := []string{"example.com", "news.example.org"}; !slices.Equal(s.TrustedLinkDomains, want) {
		t.Errorf("domains %v, want %v", s.TrustedLinkDomains, want)
	}
	if want := []string{"sam@example.com", "alex.morgan@example.com"}; !slices.Equal(s.TrustedImageSenders, want) {
		t.Errorf("senders %v, want %v", s.TrustedImageSenders, want)
	}

	// nil lists become empty (JSON [] instead of null).
	var empty models.Settings
	Normalize(&empty)
	if empty.TrustedLinkDomains == nil || empty.TrustedImageSenders == nil {
		t.Error("nil trusted lists after Normalize")
	}
}

func TestRemoteImagesAllowed(t *testing.T) {
	s := models.Settings{TrustedImageSenders: []string{"news@example.com"}}
	if !RemoteImagesAllowed(&s, "News@Example.com") {
		t.Error("trusted sender (case-insensitive) blocked")
	}
	if RemoteImagesAllowed(&s, "other@example.com") || RemoteImagesAllowed(&s, "") {
		t.Error("untrusted sender allowed")
	}
	s.ShowRemoteImages = true
	if !RemoteImagesAllowed(&s, "other@example.com") {
		t.Error("global switch ignored")
	}
}
