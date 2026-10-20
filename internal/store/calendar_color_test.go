package store

import (
	"testing"

	"github.com/AVMG20/gomail/internal/calendar"
)

// A picked color survives provider syncs; "" goes back to the provider's.
func TestCalendarColorOverride(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	if err := s.ReplaceCalendars("a1", []calendar.Calendar{{RemoteID: "r1", Name: "Work", Color: "#111111"}}); err != nil {
		t.Fatal(err)
	}
	id := CalendarID("a1", "r1")
	if err := s.SetCalendarColor(id, "#2563eb"); err != nil {
		t.Fatal(err)
	}
	_ = s.ReplaceCalendars("a1", []calendar.Calendar{{RemoteID: "r1", Name: "Work", Color: "#222222"}})
	if c, _ := s.Calendar(id); c.Color != "#2563eb" {
		t.Fatalf("after sync: %s", c.Color)
	}
	_ = s.SetCalendarColor(id, "")
	if c, _ := s.Calendar(id); c.Color != "#222222" {
		t.Fatalf("after reset: %s", c.Color)
	}
	if err := s.SetCalendarColor("nope", "#000000"); err != ErrNotFound {
		t.Fatalf("missing calendar: %v", err)
	}
}
