package config

import (
	"path/filepath"
	"testing"

	"github.com/AVMG20/gomail/internal/models"
)

func TestNormalize(t *testing.T) {
	s := models.Settings{Theme: "neon", SyncIntervalMinutes: -1, PrimaryColor: "blue", Radius: 9, ClassifyThreshold: 3,
		Labels: []models.Label{{Name: " Work "}, {Name: "work"}, {Name: "Réunions & Co"}, {ID: "none", Name: "None"}, {Name: ""}}}
	Normalize(&s)
	if s.Theme != "system" || s.SyncIntervalMinutes != 5 || s.PrimaryColor != DefaultPrimaryColor || s.Radius != 0.5 || s.ClassifyThreshold != 0.5 {
		t.Fatalf("%+v", s)
	}
	if s.MicrosoftTenant != "common" || s.ClassifierModel != DefaultClassifierModel || s.OpenRouterModel == "" {
		t.Fatalf("%+v", s)
	}
	var ids []string
	for _, l := range s.Labels {
		ids = append(ids, l.ID+":"+l.Source)
		if !IsHexColor(l.Color) {
			t.Errorf("color %q", l.Color)
		}
	}
	want := "[work:user réunions-co:user none:user junk:system]"
	if got := fmtSlice(ids); got != want {
		t.Fatalf("labels %s, want %s", got, want)
	}
}

func fmtSlice(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}

func TestDataDirOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	t.Setenv(EnvDataDir, dir)
	t.Setenv(EnvDemo, "")
	got, err := DataDir()
	if err != nil || got != dir {
		t.Fatal(got, err)
	}
	t.Setenv(EnvDemo, "1")
	got, _ = DataDir()
	if got != filepath.Join(dir, "demo") {
		t.Fatal(got)
	}
}
