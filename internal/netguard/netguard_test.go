package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "255.255.255.255": false, "::1": false, "fe80::1": false, "fc00::1": false,
		"::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false, "224.0.0.1": false, "64:ff9b::a00:1": false,
	} {
		if got := IsPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestTransportRefusesLoopback(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("http_proxy", "")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	c := &http.Client{Transport: Transport()}
	_, err := c.Post(srv.URL, "text/plain", nil)
	if !errors.Is(err, ErrBlocked) || hit {
		t.Fatalf("loopback request: err=%v hit=%v", err, hit)
	}
	// "localhost" resolves to loopback and is refused too.
	_, err = c.Get("http://localhost:" + netip.MustParseAddrPort(srv.Listener.Addr().String()).String()[len("127.0.0.1:"):])
	if !errors.Is(err, ErrBlocked) || hit {
		t.Fatalf("localhost request: err=%v hit=%v", err, hit)
	}
}
