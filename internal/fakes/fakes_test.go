package fakes

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthRejectsBadGrants(t *testing.T) {
	o := NewOAuth()
	defer o.Close()
	post := func(v url.Values) int {
		resp, err := http.PostForm(o.URL()+"/token", v)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"nope"}, "client_id": {"x"}}); c != 400 {
		t.Fatalf("unknown refresh token: %d", c)
	}
	if c := post(url.Values{"grant_type": {"authorization_code"}, "code": {"nope"}, "client_id": {"x"}}); c != 400 {
		t.Fatalf("unknown code: %d", c)
	}
	if c := post(url.Values{"grant_type": {"password"}}); c != 400 {
		t.Fatalf("no client: %d", c)
	}
	if err := o.Consent("http://127.0.0.1:1/?x=1"); err == nil || !strings.Contains(err.Error(), "redirect_uri") {
		t.Fatalf("consent without redirect: %v", err)
	}
	if err := o.Consent("http://x/auth?redirect_uri=http://127.0.0.1:1&state=s"); err == nil || !strings.Contains(err.Error(), "PKCE") {
		t.Fatalf("consent without PKCE: %v", err)
	}
}

func TestResourceServersRequireTokens(t *testing.T) {
	o := NewOAuth()
	defer o.Close()
	g := NewGmail("a@b.c", o.Valid)
	defer g.Close()
	m := NewGraph("a@b.c", "A", o.Valid)
	defer m.Close()
	for _, u := range []string{g.URL() + "/gmail/v1/users/me/profile", m.URL() + "/me"} {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s: %d", u, resp.StatusCode)
		}
	}
}
