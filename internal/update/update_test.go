package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"v1.2.4", "1.2.3", 1}, {"1.10.0", "1.9.9", 1}, {"2.0.0", "10.0.0", -1},
		{"1.0.0-beta.1", "1.0.0", -1}, {"1.0.0-beta.2", "1.0.0-beta.10", -1}, {"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.2", "1.2.0", 0}, {"1.0.0+build.5", "1.0.0", 0}, {"1.0.0-rc.1", "1.0.0-rc.1.1", -1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Error("missing accept header")
		}
		w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://github.com/AVMG20/gomail/releases/tag/v1.3.0","body":"Fixes"}`))
	}))
	defer srv.Close()
	old := LatestURL
	LatestURL = srv.URL
	defer func() { LatestURL = old }()
	info, err := Check(context.Background(), "1.2.0", nil)
	if err != nil || !info.Available || info.Latest != "1.3.0" || info.Notes != "Fixes" || info.URL == "" {
		t.Fatalf("%+v %v", info, err)
	}
	info, _ = Check(context.Background(), "1.3.0", nil)
	if info.Available {
		t.Fatal("same version reported as update")
	}
	info, err = Check(context.Background(), "dev", nil)
	if err != nil || info.Available || info.Latest != "" {
		t.Fatalf("dev build: %+v", info)
	}
}
