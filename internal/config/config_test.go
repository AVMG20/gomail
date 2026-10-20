package config

import (
	"fmt"
	"os"
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

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gomail.log")
	r, err := openRotating(path)
	if err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 1<<20)
	for i := range line {
		line[i] = 'x'
	}
	for i := 0; i < 26; i++ { // 26 MB -> several rotations
		r.Write(line)
	}
	r.Close()
	for i := 1; i <= keepLogs; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Fatalf("missing %s.%d", path, i)
		}
	}
	if _, err := os.Stat(fmt.Sprintf("%s.%d", path, keepLogs+1)); err == nil {
		t.Fatal("too many old logs kept")
	}
	if fi, _ := os.Stat(path); fi.Size() > maxLogSize {
		t.Fatalf("log too big: %d", fi.Size())
	}
}

func TestEmailBackgroundSetting(t *testing.T) {
	if DefaultSettings().EmailBackground != "auto" {
		t.Fatal("default")
	}
	for in, want := range map[string]string{"": "auto", "auto": "auto", "light": "light", "dark": "auto"} {
		s := DefaultSettings()
		s.EmailBackground = in
		Normalize(&s)
		if s.EmailBackground != want {
			t.Errorf("%q -> %q, want %q", in, s.EmailBackground, want)
		}
	}
}

func TestCalendarSettings(t *testing.T) {
	d := DefaultSettings()
	if d.CalendarWeekStart != "auto" || d.CalendarDefaultView != "week" || d.CalendarDefaultDuration != 30 || d.CalendarShowDeclined ||
		d.CalendarDefaultReminder != 10 || !d.CalendarNotifications {
		t.Fatalf("%+v", d)
	}
	s := DefaultSettings()
	s.CalendarWeekStart, s.CalendarDefaultView, s.CalendarDefaultDuration, s.CalendarDefaultReminder = "friday", "year", 1, -5
	Normalize(&s)
	if s.CalendarWeekStart != "auto" || s.CalendarDefaultView != "week" || s.CalendarDefaultDuration != 30 || s.CalendarDefaultReminder != 10 {
		t.Fatalf("%+v", s)
	}
	s.CalendarWeekStart, s.CalendarDefaultView, s.CalendarDefaultDuration, s.CalendarDefaultReminder = "monday", "agenda", 45, 0
	Normalize(&s)
	if s.CalendarWeekStart != "monday" || s.CalendarDefaultView != "agenda" || s.CalendarDefaultDuration != 45 || s.CalendarDefaultReminder != 0 {
		t.Fatalf("%+v", s)
	}
}
