package calendar

import (
	"strings"
	"testing"
	"time"
)

// invite wraps VEVENT lines (and VTIMEZONEs before it) into a REQUEST.
func invite(vtz string, event ...string) string {
	return "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\n" + vtz + "BEGIN:VEVENT\r\nUID:u1\r\nSUMMARY:Standup\r\n" +
		strings.Join(event, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func utc(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

// Outlook writes display names as TZID, quoted because of the ':' in them.
func TestInviteQuotedDisplayNameTZID(t *testing.T) {
	const tz = `"(UTC+01:00) Amsterdam, Berlin, Bern, Rome, Stockholm, Vienna"`
	for _, c := range []struct {
		start string
		want  time.Time
	}{
		{"20260310T090000", utc(2026, 3, 10, 8, 0)}, // CET
		{"20260710T090000", utc(2026, 7, 10, 7, 0)}, // CEST
	} {
		inv := ParseInvite(invite("", "DTSTART;TZID="+tz+":"+c.start, "DTEND;TZID="+tz+":"+c.start[:9]+"093000",
			`ORGANIZER;CN="Doe, Jane; PhD":mailto:jane@x.com`))
		if inv == nil || !inv.Start.Equal(c.want) || inv.End.Sub(inv.Start) != 30*time.Minute {
			t.Fatalf("%s: %+v", c.start, inv)
		}
		if inv.Organizer.Name != "Doe, Jane; PhD" || inv.Organizer.Email != "jane@x.com" {
			t.Fatalf("organizer %+v", inv.Organizer)
		}
	}
	for tzid, want := range map[string]time.Time{
		"(UTC+05:30) Chennai, Kolkata, Mumbai, New Delhi": utc(2026, 7, 10, 3, 30),
		"(UTC-05:00) Eastern Time (US & Canada)":          utc(2026, 7, 10, 13, 0),
		"(UTC-03:00) Brasilia":                            utc(2026, 7, 10, 12, 0), // no city zone: the offset
		"(UTC) Dublin, Edinburgh, Lisbon, London":         utc(2026, 7, 10, 8, 0),
		"Totally Unknown":                                 utc(2026, 7, 10, 9, 0),
	} {
		inv := ParseInvite(invite("", `DTSTART;TZID="`+tzid+`":20260710T090000`))
		if inv == nil || !inv.Start.Equal(want) {
			t.Errorf("%s: %+v, want %s", tzid, inv, want)
		}
	}
}

const outlookCustom = "BEGIN:VTIMEZONE\r\nTZID:Customized Time Zone\r\n" +
	"BEGIN:STANDARD\r\nDTSTART:16010101T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nRRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=-1SU;BYMONTH=10\r\nEND:STANDARD\r\n" +
	"BEGIN:DAYLIGHT\r\nDTSTART:16010101T020000\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0200\r\nRRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=-1SU;BYMONTH=3\r\nEND:DAYLIGHT\r\n" +
	"END:VTIMEZONE\r\n"

// Unknown TZIDs ("Customized Time Zone", Outlook for Mac) come from the
// invite's VTIMEZONE, with its DST rules.
func TestInviteCustomVTimezone(t *testing.T) {
	// Only a STANDARD part: a fixed +01:00.
	fixed := "BEGIN:VTIMEZONE\r\nTZID:Customized Time Zone\r\nBEGIN:STANDARD\r\nDTSTART:16010101T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"
	inv := ParseInvite(invite(fixed, "DTSTART;TZID=Customized Time Zone:20260310T090000", "DTEND;TZID=Customized Time Zone:20260310T100000"))
	if inv == nil || !inv.Start.Equal(utc(2026, 3, 10, 8, 0)) || inv.End.Sub(inv.Start) != time.Hour {
		t.Fatalf("fixed: %+v", inv)
	}
	sydney := "BEGIN:VTIMEZONE\r\nTZID:Sydney custom\r\n" +
		"BEGIN:STANDARD\r\nDTSTART:16010101T030000\r\nTZOFFSETFROM:+1100\r\nTZOFFSETTO:+1000\r\nRRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4\r\nEND:STANDARD\r\n" +
		"BEGIN:DAYLIGHT\r\nDTSTART:16010101T020000\r\nTZOFFSETFROM:+1000\r\nTZOFFSETTO:+1100\r\nRRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=10\r\nEND:DAYLIGHT\r\n" +
		"END:VTIMEZONE\r\n"
	// Apple style: the 2nd Sunday of March as BYDAY + BYMONTHDAY; a
	// historic rule with UNTIL that no longer applies.
	eastern := "BEGIN:VTIMEZONE\r\nTZID:US/Eastern custom\r\n" +
		"BEGIN:DAYLIGHT\r\nDTSTART:19670430T020000\r\nTZOFFSETFROM:-0500\r\nTZOFFSETTO:-0400\r\nRRULE:FREQ=YEARLY;BYMONTH=4;BYDAY=-1SU;UNTIL=19730429T070000Z\r\nEND:DAYLIGHT\r\n" +
		"BEGIN:DAYLIGHT\r\nDTSTART:20070311T020000\r\nTZOFFSETFROM:-0500\r\nTZOFFSETTO:-0400\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=SU;BYMONTHDAY=8,9,10,11,12,13,14\r\nEND:DAYLIGHT\r\n" +
		"BEGIN:STANDARD\r\nDTSTART:20071104T020000\r\nTZOFFSETFROM:-0400\r\nTZOFFSETTO:-0500\r\nRRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=1SU\r\nEND:STANDARD\r\n" +
		"END:VTIMEZONE\r\n"
	for _, c := range []struct {
		vtz, tzid, start string
		want             time.Time
	}{
		{outlookCustom, "Customized Time Zone", "20260310T090000", utc(2026, 3, 10, 8, 0)},
		{outlookCustom, "Customized Time Zone", "20260710T090000", utc(2026, 7, 10, 7, 0)},
		{outlookCustom, "Customized Time Zone", "20260328T090000", utc(2026, 3, 28, 8, 0)}, // the day before DST
		{outlookCustom, "Customized Time Zone", "20260329T090000", utc(2026, 3, 29, 7, 0)}, // the last Sunday of March
		{outlookCustom, "Customized Time Zone", "20261025T090000", utc(2026, 10, 25, 8, 0)},
		{outlookCustom, "Customized Time Zone", "20261224T090000", utc(2026, 12, 24, 8, 0)},
		{sydney, "Sydney custom", "20260115T090000", utc(2026, 1, 14, 22, 0)},
		{sydney, "Sydney custom", "20260615T090000", utc(2026, 6, 14, 23, 0)},
		{sydney, "Sydney custom", "20261005T090000", utc(2026, 10, 4, 22, 0)}, // DST from Oct 4
		{eastern, "US/Eastern custom", "20260307T090000", utc(2026, 3, 7, 14, 0)},
		{eastern, "US/Eastern custom", "20260309T090000", utc(2026, 3, 9, 13, 0)}, // DST from Mar 8
		{eastern, "US/Eastern custom", "20260420T090000", utc(2026, 4, 20, 13, 0)},
		{eastern, "US/Eastern custom", "20261110T090000", utc(2026, 11, 10, 14, 0)},
	} {
		inv := ParseInvite(invite(c.vtz, "DTSTART;TZID="+c.tzid+":"+c.start))
		if inv == nil || !inv.Start.Equal(c.want) {
			t.Errorf("%s %s: %+v, want %s", c.tzid, c.start, inv, c.want)
		}
	}
	// A VTIMEZONE after the event counts too; a known name beats it.
	after := strings.Replace(invite("", "DTSTART;TZID=Customized Time Zone:20260710T090000"), "END:VCALENDAR", outlookCustom+"END:VCALENDAR", 1)
	if inv := ParseInvite(after); inv == nil || !inv.Start.Equal(utc(2026, 7, 10, 7, 0)) {
		t.Errorf("VTIMEZONE after VEVENT: %+v", inv)
	}
	wrong := strings.Replace(outlookCustom, "Customized Time Zone", "Tokyo Standard Time", 1)
	if inv := ParseInvite(invite(wrong, "DTSTART;TZID=Tokyo Standard Time:20260710T090000")); inv == nil || !inv.Start.Equal(utc(2026, 7, 10, 0, 0)) {
		t.Errorf("known zone: %+v", inv)
	}
}

func TestICSHelpers(t *testing.T) {
	for line, want := range map[string]string{
		`DTSTART;TZID="a:b;c,d";VALUE=DATE-TIME:20260101T000000`: "DTSTART|TZID=a:b;c,d|VALUE=DATE-TIME|20260101T000000",
		`SUMMARY:a:b;c`:         "SUMMARY|a:b;c",
		`X-Y;P="unterminated:x`: "|",
		`nocolon`:               "|",
	} {
		name, params, value := splitICSLine(line)
		got := []string{name}
		for _, k := range []string{"TZID", "VALUE"} {
			if v, ok := params[k]; ok {
				got = append(got, k+"="+v)
			}
		}
		if got = append(got, value); strings.Join(got, "|") != want {
			t.Errorf("splitICSLine(%q) = %q, want %q", line, strings.Join(got, "|"), want)
		}
	}
	for v, want := range map[string]int{"+0100": 3600, "-0800": -28800, "+0530": 19800, "+053045": 19845} {
		if got, ok := utcOffset(v); !ok || got != want {
			t.Errorf("utcOffset(%q) = %d", v, got)
		}
	}
	for _, bad := range []string{"", "0100", "+1", "+01:00", "+01ab"} {
		if _, ok := utcOffset(bad); ok {
			t.Errorf("utcOffset(%q) accepted", bad)
		}
	}
	for name, want := range map[string]string{
		"(UTC+01:00) Amsterdam, Berlin, Bern, Rome, Stockholm, Vienna": "Europe/Amsterdam",
		"(UTC+10:00) Canberra, Melbourne, Sydney":                      "Australia/Canberra",
		"(GMT-08:00) Pacific Time (US & Canada)":                       "America/Los_Angeles",
		"(UTC+01:00) Tokyo":                                            "", // the offset doesn't fit
		"(UTC+02:00) Nowhere":                                          "",
		"Amsterdam":                                                    "",
	} {
		if got := IANAZone(name); got != want {
			t.Errorf("IANAZone(%q) = %q, want %q", name, got, want)
		}
	}
}
