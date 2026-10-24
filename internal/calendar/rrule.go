package calendar

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

var byDay = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// BuildRRule returns the RRULE (without "RRULE:") for a simple recurrence,
// or "" for none. Weekly repeats on the start's weekday, monthly on its day
// of the month, yearly on its date (all in the event's zone). Until is the
// last day as a calendar date (00:00 UTC, like all-day dates).
func BuildRRule(r models.EventRecurrence, start time.Time, tz string, allDay bool) string {
	freq := map[string]string{models.RepeatDaily: "DAILY", models.RepeatWeekly: "WEEKLY",
		models.RepeatMonthly: "MONTHLY", models.RepeatYearly: "YEARLY"}[r.Frequency]
	if freq == "" {
		return ""
	}
	loc := ZoneLoc(tz)
	if allDay {
		loc = time.UTC // all-day dates are 00:00 UTC
	}
	parts := []string{"FREQ=" + freq}
	if freq == "WEEKLY" {
		parts = append(parts, "BYDAY="+byDay[start.In(loc).Weekday()])
	}
	switch {
	case r.Count > 0:
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	case r.Until != nil && !r.Until.IsZero():
		parts = append(parts, "UNTIL="+UntilValue(*r.Until, loc, allDay))
	}
	return strings.Join(parts, ";")
}

// ZoneLoc loads an IANA (or Windows) zone; UTC when empty or unknown.
func ZoneLoc(tz string) *time.Location {
	if z := IANAZone(tz); z != "" {
		if l, err := time.LoadLocation(z); err == nil {
			return l
		}
	}
	return time.UTC
}

// UntilValue renders the inclusive last day of a series (the calendar date
// of day) as an RRULE UNTIL: the end of that day in the event's zone, in
// UTC, or a DATE for all-day events (RFC 5545: UNTIL takes DTSTART's type).
func UntilValue(day time.Time, loc *time.Location, allDay bool) string {
	y, m, d := day.Date()
	if allDay {
		return fmt.Sprintf("%04d%02d%02d", y, int(m), d)
	}
	end := time.Date(y, m, d+1, 0, 0, 0, 0, loc).Add(-time.Second)
	return end.UTC().Format("20060102T150405Z")
}

// UntilDay returns the last day an RRULE UNTIL allows, at 00:00 UTC: a UTC
// time is read in the event's zone (the day of the last possible
// occurrence), a DATE or a floating time is taken as is. UntilDay of
// UntilValue gives the same day back.
func UntilDay(v string, loc *time.Location) (time.Time, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		if err != nil {
			return time.Time{}, false
		}
		return DateUTC(t.In(loc)), true
	}
	if len(v) < 8 {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102", v[:8])
	return t, err == nil
}

var dayNames = map[string]string{"MO": "Monday", "TU": "Tuesday", "WE": "Wednesday", "TH": "Thursday", "FR": "Friday", "SA": "Saturday", "SU": "Sunday"}

// Summarize renders an RRULE as text, e.g. "Weekly on Monday, 10 times".
// Unknown rules give "Repeats". A UTC UNTIL is read in UTC; SummarizeIn
// reads it in the event's zone.
func Summarize(rule string) string {
	return SummarizeIn(rule, "")
}

// SummarizeIn is Summarize for an event in zone tz (IANA or Windows).
func SummarizeIn(rule, tz string) string {
	rule = strings.TrimPrefix(strings.TrimSpace(rule), "RRULE:")
	if rule == "" {
		return ""
	}
	kv := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		k, v, _ := strings.Cut(p, "=")
		kv[strings.ToUpper(k)] = v
	}
	interval, _ := strconv.Atoi(kv["INTERVAL"])
	unit := map[string]string{"DAILY": "day", "WEEKLY": "week", "MONTHLY": "month", "YEARLY": "year"}[kv["FREQ"]]
	if unit == "" {
		return "Repeats"
	}
	var s string
	if interval > 1 {
		s = fmt.Sprintf("Every %d %ss", interval, unit)
	} else {
		s = map[string]string{"day": "Daily", "week": "Weekly", "month": "Monthly", "year": "Yearly"}[unit]
	}
	if days := kv["BYDAY"]; days != "" && unit == "week" {
		var names []string
		for _, d := range strings.Split(days, ",") {
			if n := dayNames[strings.ToUpper(d[max(0, len(d)-2):])]; n != "" {
				names = append(names, n)
			}
		}
		switch {
		case days == "MO,TU,WE,TH,FR":
			s += " on weekdays"
		case len(names) > 0:
			s += " on " + joinAnd(names)
		}
	}
	if c := kv["COUNT"]; c != "" {
		s += ", " + c + " times"
	} else if u := kv["UNTIL"]; u != "" {
		if t, ok := UntilDay(u, ZoneLoc(tz)); ok {
			s += ", until " + t.Format("Jan 2, 2006")
		}
	}
	return s
}

func joinAnd(xs []string) string {
	if len(xs) <= 1 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}
