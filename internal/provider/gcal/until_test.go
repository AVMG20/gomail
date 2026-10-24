package gcal

import (
	"testing"

	"github.com/AVMG20/gomail/internal/calendar"
)

// A series made in Google Calendar "until Nov 30" in Los Angeles carries
// UNTIL=20261201T075959Z: it ends on Nov 30, not Dec 1.
func TestUntilSummaryInTheEventZone(t *testing.T) {
	c := &Client{}
	cal := calendar.Calendar{RemoteID: "primary", TimeZone: "America/Los_Angeles"}
	timed := c.toEvent(gEvent{ID: "a", Recurrence: []string{"RRULE:FREQ=DAILY;UNTIL=20261201T075959Z"},
		Start: gTime{DateTime: "2026-11-23T09:00:00-08:00", TimeZone: "America/Los_Angeles"},
		End:   gTime{DateTime: "2026-11-23T10:00:00-08:00", TimeZone: "America/Los_Angeles"}}, cal)
	allDay := c.toEvent(gEvent{ID: "b", Recurrence: []string{"RRULE:FREQ=DAILY;UNTIL=20261201T075959Z"},
		Start: gTime{Date: "2026-11-23"}, End: gTime{Date: "2026-11-24"}}, cal)
	for _, ev := range []calendar.Event{timed, allDay} {
		if ev.Recurrence != "Daily, until Nov 30, 2026" {
			t.Errorf("%s: %q", ev.RemoteID, ev.Recurrence)
		}
	}
}
