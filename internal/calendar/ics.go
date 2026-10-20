package calendar

import (
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// ParseInvite reads the first VEVENT of an iCalendar (RFC 5545) invitation.
// It returns nil when the text has no usable event.
func ParseInvite(ics string) *models.Invite {
	if len(ics) > 1<<20 {
		ics = ics[:1<<20]
	}
	// Unfold continuation lines.
	ics = strings.NewReplacer("\r\n ", "", "\r\n\t", "", "\n ", "", "\n\t", "").Replace(ics)
	inv := &models.Invite{Method: "PUBLISH"}
	inEvent, done := false, false
	var startTZ, endTZ, startRaw, endRaw string
	var startDate, endDate bool
	for _, line := range strings.Split(strings.ReplaceAll(ics, "\r\n", "\n"), "\n") {
		if done {
			break
		}
		name, params, value := splitICSLine(line)
		switch {
		case name == "METHOD" && !inEvent:
			inv.Method = strings.ToUpper(value)
		case name == "BEGIN" && strings.EqualFold(value, "VEVENT"):
			inEvent = true
		case name == "END" && strings.EqualFold(value, "VEVENT"):
			done = true
		case !inEvent:
		case name == "UID":
			inv.UID = value
		case name == "SUMMARY":
			inv.Title = icsText(value)
		case name == "LOCATION":
			inv.Location = icsText(value)
		case name == "DTSTART":
			startRaw, startTZ, startDate = value, params["TZID"], params["VALUE"] == "DATE" || len(value) == 8
		case name == "DTEND":
			endRaw, endTZ, endDate = value, params["TZID"], params["VALUE"] == "DATE" || len(value) == 8
		case name == "ORGANIZER":
			inv.Organizer = models.Address{Name: strings.Trim(params["CN"], `"`), Email: strings.TrimPrefix(strings.TrimPrefix(value, "mailto:"), "MAILTO:")}
		}
	}
	if inv.UID == "" || startRaw == "" {
		return nil
	}
	inv.Start, inv.AllDay = icsTime(startRaw, startTZ, startDate)
	if endRaw != "" {
		inv.End, _ = icsTime(endRaw, endTZ, endDate)
	}
	if inv.Start.IsZero() {
		return nil
	}
	if !inv.End.After(inv.Start) {
		if inv.AllDay {
			inv.End = inv.Start.AddDate(0, 0, 1)
		} else {
			inv.End = inv.Start.Add(time.Hour)
		}
	}
	if len(inv.Title) > 1000 {
		inv.Title = inv.Title[:1000]
	}
	return inv
}

// splitICSLine splits "NAME;P1=V1;P2=V2:VALUE".
func splitICSLine(line string) (string, map[string]string, string) {
	line = strings.TrimRight(line, "\r")
	head, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", nil, ""
	}
	parts := strings.Split(head, ";")
	params := map[string]string{}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		params[strings.ToUpper(k)] = v
	}
	return strings.ToUpper(parts[0]), params, value
}

func icsText(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// icsTime parses DATE, DATE-TIME (UTC "Z", floating, or with TZID; TZID
// may be a Windows name).
func icsTime(v, tzid string, date bool) (time.Time, bool) {
	if date {
		t, err := time.Parse("20060102", v[:min(8, len(v))])
		if err != nil {
			return time.Time{}, true
		}
		return t, true
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), false
	}
	loc := time.UTC
	if z := IANAZone(strings.Trim(tzid, `"`)); z != "" {
		if l, err := time.LoadLocation(z); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), false
}

// BuildInvite renders a minimal iCalendar REQUEST (demo data, tests).
func BuildInvite(uid, title, location string, organizer models.Address, start, end time.Time) string {
	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`)
	f := func(t time.Time) string { return t.UTC().Format("20060102T150405Z") }
	return strings.Join([]string{
		"BEGIN:VCALENDAR", "PRODID:-//gomail//demo//EN", "VERSION:2.0", "METHOD:REQUEST", "BEGIN:VEVENT",
		"UID:" + uid, "SUMMARY:" + esc.Replace(title), "LOCATION:" + esc.Replace(location),
		"DTSTART:" + f(start), "DTEND:" + f(end), "ORGANIZER;CN=" + organizer.Name + ":mailto:" + organizer.Email,
		"END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
}
