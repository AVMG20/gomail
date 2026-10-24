package calendar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// fromWire runs an EventInput as the frontend sends it through
// NormalizeInput and FromInput.
func fromWire(t *testing.T, js string) Event {
	t.Helper()
	var in models.EventInput
	if err := json.Unmarshal([]byte(js), &in); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeInput(&in); err != nil {
		t.Fatal(err)
	}
	return FromInput(in)
}

func untilOf(rule string) string {
	for _, p := range strings.Split(rule, ";") {
		if v, ok := strings.CutPrefix(p, "UNTIL="); ok {
			return v
		}
	}
	return ""
}

// "Repeat until Nov 30" ends at the end of Nov 30 in the event's zone, on
// both sides of UTC, and reads back as Nov 30.
func TestUntilIsTheLastDayInTheEventZone(t *testing.T) {
	for _, c := range []struct {
		name, js, until string
		last, after     time.Time // the last occurrence, the first one after it
	}{
		{"Los Angeles daily 09:00",
			`{"start":"2026-11-23T17:00:00Z","end":"2026-11-23T18:00:00Z","timeZone":"America/Los_Angeles",
			  "recurrence":{"frequency":"daily","until":"2026-11-30T00:00:00Z"}}`,
			"20261201T075959Z", time.Date(2026, 11, 30, 17, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 17, 0, 0, 0, time.UTC)},
		{"Sydney daily 08:00",
			`{"start":"2026-11-23T21:00:00Z","end":"2026-11-23T22:00:00Z","timeZone":"Australia/Sydney",
			  "recurrence":{"frequency":"daily","until":"2026-11-30T00:00:00Z"}}`,
			"20261130T125959Z", time.Date(2026, 11, 29, 21, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 21, 0, 0, 0, time.UTC)},
		{"UTC", `{"start":"2026-11-23T09:00:00Z","recurrence":{"frequency":"weekly","until":"2026-11-30T00:00:00Z"}}`,
			"20261130T235959Z", time.Time{}, time.Time{}},
	} {
		ev := fromWire(t, c.js)
		u := untilOf(ev.RRule)
		if u != c.until || ev.Recurrence == "" || !strings.HasSuffix(ev.Recurrence, "until Nov 30, 2026") {
			t.Errorf("%s: %s (%q), want UNTIL=%s", c.name, ev.RRule, ev.Recurrence, c.until)
		}
		end, _ := time.Parse("20060102T150405Z", u)
		if !c.last.IsZero() && (c.last.After(end) || !c.after.After(end)) {
			t.Errorf("%s: UNTIL %s must allow %s but not %s", c.name, end, c.last, c.after)
		}
		if d, ok := UntilDay(u, ZoneLoc(ev.TimeZone)); !ok || d.Format("2006-01-02") != "2026-11-30" {
			t.Errorf("%s: reads back as %s", c.name, d)
		}
	}
	// All-day: a DATE, like DTSTART (RFC 5545), whatever zone comes along.
	ev := fromWire(t, `{"start":"2026-11-23T00:00:00Z","allDay":true,"timeZone":"America/Los_Angeles",
	  "recurrence":{"frequency":"weekly","until":"2026-11-30T00:00:00Z"}}`)
	if ev.RRule != "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261130" || ev.Recurrence != "Weekly on Monday, until Nov 30, 2026" {
		t.Errorf("all-day: %s (%q)", ev.RRule, ev.Recurrence)
	}
}

// Rules from elsewhere: a UTC UNTIL is the day of the last possible
// occurrence in the event's zone.
func TestUntilDayAndSummary(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	syd, _ := time.LoadLocation("Australia/Sydney")
	for _, c := range []struct {
		v    string
		loc  *time.Location
		want string
	}{
		{"20261201T075959Z", la, "2026-11-30"}, // Google web, "until Nov 30" in LA
		{"20261130T170000Z", la, "2026-11-30"}, // the last start itself
		{"20261130T125959Z", syd, "2026-11-30"},
		{"20261130T235959Z", syd, "2026-12-01"}, // old gomail rule: Dec 1 08:00 did occur
		{"20261130T235959z", time.UTC, "2026-11-30"},
		{"20261130", la, "2026-11-30"},
		{"20261130T090000", syd, "2026-11-30"}, // floating
		{"", la, ""}, {"2026", la, ""}, {"2026113xT000000Z", la, ""},
	} {
		d, ok := UntilDay(c.v, c.loc)
		if got := map[bool]string{true: d.Format("2006-01-02")}[ok]; got != c.want {
			t.Errorf("UntilDay(%q, %s) = %q, want %q", c.v, c.loc, got, c.want)
		}
	}
	if got := SummarizeIn("FREQ=DAILY;UNTIL=20261201T075959Z", "America/Los_Angeles"); got != "Daily, until Nov 30, 2026" {
		t.Errorf("LA summary %q", got)
	}
	if got := SummarizeIn("FREQ=DAILY;UNTIL=20261201T075959Z", "Pacific Standard Time"); got != "Daily, until Nov 30, 2026" {
		t.Errorf("Windows zone summary %q", got)
	}
	if got := Summarize("FREQ=DAILY;UNTIL=20261130"); got != "Daily, until Nov 30, 2026" {
		t.Errorf("DATE summary %q", got)
	}
}

// Saving a series again (title edit, drag) keeps its end date: reading the
// UNTIL back and building it again gives the same rule, every day of the
// year in zones with odd offsets and DST at midnight.
func TestUntilRoundTripIsStable(t *testing.T) {
	for _, tz := range []string{"UTC", "America/Los_Angeles", "Australia/Sydney", "Australia/Lord_Howe", "America/Santiago",
		"Asia/Beirut", "Pacific/Kiritimati", "Pacific/Pago_Pago", "Asia/Kathmandu", "Europe/Amsterdam"} {
		loc := ZoneLoc(tz)
		for d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); d.Year() == 2026; d = d.AddDate(0, 0, 1) {
			for _, allDay := range []bool{false, true} {
				v := UntilValue(d, loc, allDay)
				back, ok := UntilDay(v, loc)
				if !ok || !back.Equal(d) || UntilValue(back, loc, allDay) != v {
					t.Fatalf("%s %s allDay=%v: %s reads back as %s", tz, d.Format("2006-01-02"), allDay, v, back)
				}
			}
		}
	}
}
