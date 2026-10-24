package calendar

import (
	"strconv"
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
	lines := strings.Split(strings.ReplaceAll(ics, "\r\n", "\n"), "\n")
	inv := &models.Invite{Method: "PUBLISH"}
	inEvent, done := false, false
	var startTZ, endTZ, startRaw, endRaw string
	var startDate, endDate bool
	for _, line := range lines {
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
			inv.Organizer = models.Address{Name: params["CN"], Email: strings.TrimPrefix(strings.TrimPrefix(value, "mailto:"), "MAILTO:")}
		}
	}
	if inv.UID == "" || startRaw == "" {
		return nil
	}
	zones := vtimezones(lines)
	inv.Start, inv.AllDay = icsTime(startRaw, startTZ, startDate, zones)
	if endRaw != "" {
		inv.End, _ = icsTime(endRaw, endTZ, endDate, zones)
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

// splitICSLine splits `NAME;P1=V1;P2="V:2":VALUE`. Parameter values may be
// quoted and then hold ':', ';' and ',' (RFC 5545 3.1); quotes are removed.
func splitICSLine(line string) (string, map[string]string, string) {
	line = strings.TrimRight(line, "\r")
	var fields []string
	quoted, from := false, 0
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == ';':
			fields, from = append(fields, line[from:i]), i+1
		case c == ':':
			fields = append(fields, line[from:i])
			params := map[string]string{}
			for _, p := range fields[1:] {
				k, v, _ := strings.Cut(p, "=")
				params[strings.ToUpper(k)] = strings.ReplaceAll(v, `"`, "")
			}
			return strings.ToUpper(fields[0]), params, line[i+1:]
		}
	}
	return "", nil, ""
}

func icsText(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// icsTime parses DATE, DATE-TIME (UTC "Z", floating, or with TZID). A TZID
// is an IANA or Windows name, else the invite's VTIMEZONE of that name, else
// a Windows display name ("(UTC+01:00) Amsterdam, ..."); unknown is UTC.
func icsTime(v, tzid string, date bool, zones map[string]vtimezone) (time.Time, bool) {
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
	wall, err := time.Parse("20060102T150405", v)
	if err != nil {
		return time.Time{}, false
	}
	tzid = strings.TrimSpace(tzid)
	if z := knownZone(tzid); z != "" {
		if loc, err := time.LoadLocation(z); err == nil {
			return inZone(wall, loc), false
		}
	}
	if vz, ok := zones[tzid]; ok {
		if off, ok := vz.offsetAt(wall); ok {
			return wall.Add(-time.Duration(off) * time.Second), false
		}
	}
	if z := displayZone(tzid); z != "" {
		if loc, err := time.LoadLocation(z); err == nil {
			return inZone(wall, loc), false
		}
	}
	if off, ok := displayOffset(tzid); ok {
		return wall.Add(-time.Duration(off) * time.Second), false
	}
	return wall, false
}

// inZone reads a wall clock time (given in UTC) in loc.
func inZone(wall time.Time, loc *time.Location) time.Time {
	y, mo, d := wall.Date()
	h, mi, s := wall.Clock()
	return time.Date(y, mo, d, h, mi, s, 0, loc).UTC()
}

// vtimezone is a VTIMEZONE: its STANDARD and DAYLIGHT observances.
type vtimezone []observance

// observance is one STANDARD or DAYLIGHT part; times are wall clock (in
// UTC). With a yearly RRULE, the onset is in month on the ord'th weekday
// (ord < 0 counts from the end), or on the first of monthDays that is that
// weekday; weekday -1 = on monthDays (or DTSTART's day) whatever weekday.
type observance struct {
	start     time.Time // DTSTART, the first onset
	from, to  int       // TZOFFSETFROM, TZOFFSETTO in seconds
	yearly    bool
	month     time.Month
	weekday   time.Weekday
	ord       int
	monthDays []int
	until     time.Time
	rdates    []time.Time
	rule      string // RRULE, read at the end (UNTIL needs TZOFFSETFROM)
}

// vtimezones reads the invite's VTIMEZONEs by TZID.
func vtimezones(lines []string) map[string]vtimezone {
	zones := map[string]vtimezone{}
	var tzid string
	var cur *observance
	var obs vtimezone
	inTZ := false
	for _, line := range lines {
		name, _, value := splitICSLine(line)
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VTIMEZONE"):
			inTZ, tzid, obs, cur = true, "", nil, nil
		case !inTZ:
		case name == "END" && strings.EqualFold(value, "VTIMEZONE"):
			if tzid != "" && len(obs) > 0 && len(zones) < 20 {
				zones[tzid] = obs
			}
			inTZ = false
		case name == "TZID" && cur == nil:
			tzid = strings.TrimSpace(value)
		case name == "BEGIN" && (strings.EqualFold(value, "STANDARD") || strings.EqualFold(value, "DAYLIGHT")):
			cur = &observance{weekday: -1}
		case cur == nil:
		case name == "END":
			if !cur.start.IsZero() && len(obs) < 50 {
				cur.parseRule()
				obs = append(obs, *cur)
			}
			cur = nil
		case name == "DTSTART":
			cur.start, _ = time.Parse("20060102T150405", value)
		case name == "TZOFFSETFROM":
			cur.from, _ = utcOffset(value)
		case name == "TZOFFSETTO":
			cur.to, _ = utcOffset(value)
		case name == "RDATE":
			for _, v := range strings.Split(value, ",") {
				if t, err := time.Parse("20060102T150405", v); err == nil && len(cur.rdates) < 100 {
					cur.rdates = append(cur.rdates, t)
				}
			}
		case name == "RRULE":
			cur.rule = value
		}
	}
	return zones
}

// parseRule reads the yearly rules VTIMEZONEs use, e.g.
// "FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU" or "...;BYDAY=SU;BYMONTHDAY=8,9,...".
func (o *observance) parseRule() {
	o.month = o.start.Month()
	for _, p := range strings.Split(o.rule, ";") {
		k, v, _ := strings.Cut(p, "=")
		v = strings.ToUpper(strings.TrimSpace(v))
		switch strings.ToUpper(k) {
		case "FREQ":
			o.yearly = v == "YEARLY"
		case "BYMONTH":
			if m, err := strconv.Atoi(v); err == nil && m >= 1 && m <= 12 {
				o.month = time.Month(m)
			}
		case "BYDAY":
			for i, d := range byDay {
				if len(v) >= 2 && strings.HasSuffix(v, d) {
					o.weekday = time.Weekday(i)
					o.ord, _ = strconv.Atoi(v[:len(v)-2])
				}
			}
		case "BYMONTHDAY":
			for _, d := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= 31 {
					o.monthDays = append(o.monthDays, n)
				}
			}
		case "UNTIL":
			if t, err := time.Parse("20060102T150405Z", v); err == nil {
				o.until = t.Add(time.Duration(o.from) * time.Second) // UTC -> wall clock
			} else if t, err := time.Parse("20060102T150405", v); err == nil {
				o.until = t
			}
		}
	}
}

// onset is the observance's onset in year (wall clock).
func (o observance) onset(year int) (time.Time, bool) {
	h, mi, s := o.start.Clock()
	at := func(d int) time.Time { return time.Date(year, o.month, d, h, mi, s, 0, time.UTC) }
	last := time.Date(year, o.month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	switch {
	case o.weekday < 0 && len(o.monthDays) > 0:
		return at(min(o.monthDays[0], last)), true
	case o.weekday < 0:
		return at(min(o.start.Day(), last)), true
	case len(o.monthDays) > 0:
		for _, d := range o.monthDays {
			if d <= last && at(d).Weekday() == o.weekday {
				return at(d), true
			}
		}
		return time.Time{}, false
	}
	ord := o.ord
	if ord == 0 {
		ord = 1
	}
	var d int
	if ord > 0 {
		d = 1 + (int(o.weekday)-int(at(1).Weekday())+7)%7 + (ord-1)*7
		for d > last { // "5th Sunday": the last one
			d -= 7
		}
	} else {
		d = last - (int(at(last).Weekday())-int(o.weekday)+7)%7 + (ord+1)*7
		for d < 1 {
			d += 7
		}
	}
	return at(d), true
}

// offsetAt returns the UTC offset (seconds) at a wall clock time: that of
// the observance with the latest onset at or before it.
func (z vtimezone) offsetAt(wall time.Time) (int, bool) {
	var best time.Time
	off, found := 0, false
	consider := func(on time.Time, o observance) {
		if on.After(wall) || on.Before(o.start) || (!o.until.IsZero() && on.After(o.until)) {
			return
		}
		if !found || on.After(best) {
			best, off, found = on, o.to, true
		}
	}
	for _, o := range z {
		consider(o.start, o)
		for _, r := range o.rdates {
			consider(r, o)
		}
		if o.yearly {
			for y := wall.Year() - 1; y <= wall.Year(); y++ {
				if on, ok := o.onset(y); ok {
					consider(on, o)
				}
			}
		}
	}
	if !found && len(z) > 0 {
		// Before every onset: the offset the earliest one changes from.
		first := z[0]
		for _, o := range z[1:] {
			if o.start.Before(first.start) {
				first = o
			}
		}
		return first.from, true
	}
	return off, found
}

// utcOffset parses "+0100", "-0800" or "+053000" into seconds.
func utcOffset(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if (len(v) != 5 && len(v) != 7) || (v[0] != '+' && v[0] != '-') {
		return 0, false
	}
	n := 0
	for i, part := range []string{v[1:3], v[3:5], v[5:]} {
		if part == "" {
			continue
		}
		x, err := strconv.Atoi(part)
		if err != nil {
			return 0, false
		}
		n += x * []int{3600, 60, 1}[i]
	}
	if v[0] == '-' {
		n = -n
	}
	return n, true
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
