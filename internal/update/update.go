// Package update checks GitHub releases for a newer gomail version.
package update

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
)

// LatestURL is the GitHub API endpoint for the latest release.
var LatestURL = "https://api.github.com/repos/AVMG20/gomail/releases/latest"

type version struct {
	nums [3]int
	pre  string
}

// parse reads "v1.2.3", "1.2", "1.2.3-beta.1"; ok is false otherwise.
func parse(s string) (version, bool) {
	var v version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+") // build metadata is ignored
	core, pre, _ := strings.Cut(s, "-")
	v.pre = pre
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	return v, true
}

// Compare returns -1, 0 or 1 (semver precedence; prereleases sort before
// the release, compared lexically by dot-separated identifiers).
func Compare(a, b string) int {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return strings.Compare(a, b)
	}
	for i := range va.nums {
		if va.nums[i] != vb.nums[i] {
			if va.nums[i] < vb.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case va.pre == vb.pre:
		return 0
	case va.pre == "":
		return 1
	case vb.pre == "":
		return -1
	}
	pa, pb := strings.Split(va.pre, "."), strings.Split(vb.pre, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case ea != nil || eb != nil:
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}

// Check asks GitHub for the latest release. Development builds ("dev" or
// unparsable versions) never report an update.
func Check(ctx context.Context, current string, client *http.Client) (models.UpdateInfo, error) {
	info := models.UpdateInfo{Current: current}
	if _, ok := parse(current); !ok {
		return info, nil
	}
	api := &httpx.Client{HTTP: client, Service: "GitHub", MaxRetries: 2,
		Headers: map[string]string{"Accept": "application/vnd.github+json", "User-Agent": "gomail/" + current}}
	var rel struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := api.JSON(ctx, http.MethodGet, LatestURL, nil, &rel); err != nil {
		if httpx.StatusOf(err) == http.StatusNotFound {
			return info, nil // no releases yet
		}
		return info, err
	}
	if rel.Draft || rel.TagName == "" {
		return info, nil
	}
	info.Latest, info.URL, info.Notes = strings.TrimPrefix(rel.TagName, "v"), rel.HTMLURL, rel.Body
	info.Available = Compare(info.Latest, current) > 0
	return info, nil
}
