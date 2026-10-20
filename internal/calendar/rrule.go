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
// of the month, yearly on its date (all in the event's zone).
func BuildRRule(r models.EventRecurrence, start time.Time, tz string) string {
	freq := map[string]string{models.RepeatDaily: "DAILY", models.RepeatWeekly: "WEEKLY",
		models.RepeatMonthly: "MONTHLY", models.RepeatYearly: "YEARLY"}[r.Frequency]
	if freq == "" {
		return ""
	}
	loc := time.UTC
	if l, err := time.LoadLocation(tz); err == nil && tz != "" {
		loc = l
	}
	parts := []string{"FREQ=" + freq}
	if freq == "WEEKLY" {
		parts = append(parts, "BYDAY="+byDay[start.In(loc).Weekday()])
	}
	switch {
	case r.Count > 0:
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	case r.Until != nil && !r.Until.IsZero():
		// UNTIL is inclusive; use the end of that day in UTC.
		u := DateUTC(*r.Until).Add(24*time.Hour - time.Second)
		parts = append(parts, "UNTIL="+u.Format("20060102T150405Z"))
	}
	return strings.Join(parts, ";")
}

var dayNames = map[string]string{"MO": "Monday", "TU": "Tuesday", "WE": "Wednesday", "TH": "Thursday", "FR": "Friday", "SA": "Saturday", "SU": "Sunday"}

// Summarize renders an RRULE as text, e.g. "Weekly on Monday, 10 times".
// Unknown rules give "Repeats".
func Summarize(rule string) string {
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
	} else if u := kv["UNTIL"]; len(u) >= 8 {
		if t, err := time.Parse("20060102", u[:8]); err == nil {
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
