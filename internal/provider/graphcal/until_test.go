package graphcal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/models"
)

// "Repeat until Nov 30" is Outlook's endDate 2026-11-30 east and west of
// UTC, reads back as Nov 30 and is written back unchanged.
func TestUntilEndDateRoundTrip(t *testing.T) {
	until := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		tz, win string
		start   time.Time
		allDay  bool
		rule    string
	}{
		{"Australia/Sydney", "AUS Eastern Standard Time", time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC), false, "FREQ=DAILY;UNTIL=20261130T125959Z"},
		{"America/Los_Angeles", "Pacific Standard Time", time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC), false, "FREQ=DAILY;UNTIL=20261201T075959Z"},
		{"America/Los_Angeles", "Pacific Standard Time", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), true, "FREQ=DAILY;UNTIL=20261130"},
	} {
		in := models.EventInput{Title: "x", Start: c.start, AllDay: c.allDay, TimeZone: c.tz,
			Recurrence: models.EventRecurrence{Frequency: models.RepeatDaily, Until: &until}}
		if err := calendar.NormalizeInput(&in); err != nil {
			t.Fatal(err)
		}
		ev := calendar.FromInput(in)
		if ev.RRule != c.rule {
			t.Errorf("%s allDay=%v: rule %s, want %s", c.tz, c.allDay, ev.RRule, c.rule)
		}
		b := body(ev, true, "")
		rng := b["recurrence"].(map[string]any)["range"].(map[string]any)
		start := "2026-11-24"
		if c.tz == "America/Los_Angeles" {
			start = "2026-11-23"
		}
		if rng["endDate"] != "2026-11-30" || rng["startDate"] != start || rng["recurrenceTimeZone"] != c.win {
			t.Errorf("%s allDay=%v: range %v", c.tz, c.allDay, rng)
		}
		// Back from Graph: the same rule, so another save changes nothing.
		js, _ := json.Marshal(b["recurrence"])
		var r gRecurrence
		if err := json.Unmarshal(js, &r); err != nil {
			t.Fatal(err)
		}
		if got := rruleOf(&r, c.tz, c.allDay); got != c.rule {
			t.Errorf("%s allDay=%v: read back %s, want %s", c.tz, c.allDay, got, c.rule)
		}
		g := gEvent{ID: "s", Type: "seriesMaster", IsAllDay: c.allDay, OriginalStartTimeZone: c.win, Recurrence: &r,
			Start: gTime{DateTime: "2026-11-23T00:00:00.0000000"}, End: gTime{DateTime: "2026-11-24T00:00:00.0000000"}}
		if got := toEvent(g, calendar.Calendar{}).Recurrence; got != "Daily, until Nov 30, 2026" {
			t.Errorf("%s allDay=%v: summary %q", c.tz, c.allDay, got)
		}
	}
	// A rule without its own zone uses the event's.
	var r gRecurrence
	r.Pattern.Type, r.Range.Type, r.Range.EndDate = "weekly", "endDate", "2026-11-30"
	if got := rruleOf(&r, "Australia/Sydney", false); got != "FREQ=WEEKLY;UNTIL=20261130T125959Z" {
		t.Errorf("event zone: %s", got)
	}
}
