package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

func TestZones(t *testing.T) {
	for in, want := range map[string]string{
		"W. Europe Standard Time": "Europe/Berlin", "Pacific Standard Time": "America/Los_Angeles", "Tokyo Standard Time": "Asia/Tokyo",
		"Europe/Amsterdam": "Europe/Amsterdam", "UTC": "UTC", "Coordinated Universal Time": "UTC", "Not A Zone": "", "": "",
	} {
		if got := IANAZone(in); got != want {
			t.Errorf("IANAZone(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Europe/Amsterdam": "W. Europe Standard Time", "America/New_York": "Eastern Standard Time",
		"UTC": "UTC", "Asia/Tokyo": "Tokyo Standard Time", "Mars/Olympus": ""} {
		if got := WindowsZone(in); got != want {
			t.Errorf("WindowsZone(%q) = %q, want %q", in, got, want)
		}
	}
	for w, z := range windowsZones {
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("%s -> %s doesn't load: %v", w, z, err)
		}
	}
}

func TestRRules(t *testing.T) {
	until := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC) // Tuesday 01:30 in Amsterdam
	for _, c := range []struct {
		r    models.EventRecurrence
		tz   string
		want string
		sum  string
	}{
		{models.EventRecurrence{Frequency: models.RepeatNone}, "", "", ""},
		{models.EventRecurrence{Frequency: models.RepeatDaily, Count: 5}, "", "FREQ=DAILY;COUNT=5", "Daily, 5 times"},
		{models.EventRecurrence{Frequency: models.RepeatWeekly}, "UTC", "FREQ=WEEKLY;BYDAY=MO", "Weekly on Monday"},
		{models.EventRecurrence{Frequency: models.RepeatWeekly}, "Europe/Amsterdam", "FREQ=WEEKLY;BYDAY=TU", "Weekly on Tuesday"},
		{models.EventRecurrence{Frequency: models.RepeatMonthly, Until: &until}, "", "FREQ=MONTHLY;UNTIL=20261231T235959Z", "Monthly, until Dec 31, 2026"},
		{models.EventRecurrence{Frequency: models.RepeatYearly}, "", "FREQ=YEARLY", "Yearly"},
	} {
		got := BuildRRule(c.r, mon, c.tz, false)
		if got != c.want || Summarize(got) != c.sum {
			t.Errorf("%+v: %q (%q), want %q (%q)", c.r, got, Summarize(got), c.want, c.sum)
		}
	}
	for rule, want := range map[string]string{
		"RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE": "Every 2 weeks on Monday and Wednesday",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":         "Weekly on weekdays",
		"FREQ=SECONDLY":                            "Repeats",
	} {
		if got := Summarize(rule); got != want {
			t.Errorf("Summarize(%q) = %q, want %q", rule, got, want)
		}
	}
}

func TestNormalizeInputAndWindow(t *testing.T) {
	in := models.EventInput{Title: "x", Start: time.Date(2026, 10, 20, 15, 4, 0, 0, time.FixedZone("x", 3600)), TimeZone: "Nope/Zone"}
	if err := NormalizeInput(&in); err != nil || in.End.Sub(in.Start) != time.Hour || in.TimeZone != "" || in.Start.Location() != time.UTC ||
		in.Recurrence.Frequency != models.RepeatNone {
		t.Fatalf("%+v %v", in, err)
	}
	in = models.EventInput{Start: time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC), AllDay: true, End: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}
	if err := NormalizeInput(&in); err != nil || !in.Start.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) || in.End.Sub(in.Start) != 48*time.Hour {
		t.Fatalf("all-day %+v %v", in, err)
	}
	for _, bad := range []models.EventInput{
		{}, {Start: time.Now(), End: time.Now().Add(-time.Minute)}, {Start: time.Now(), Recurrence: models.EventRecurrence{Frequency: "x"}},
		{Start: time.Now(), ReminderMinutes: []int{-1}}, {Start: time.Now(), Recurrence: models.EventRecurrence{Frequency: models.RepeatDaily, Count: 5000}},
	} {
		if NormalizeInput(&bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	s, e := WindowFor(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if !s.Equal(time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)) || !e.Equal(time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window %v %v", s, e)
	}
	ev := FromInput(models.EventInput{Title: "t", Start: s, End: e, Recurrence: models.EventRecurrence{Frequency: models.RepeatDaily}})
	if !ev.IsRecurring || ev.RRule != "FREQ=DAILY" || ev.Recurrence != "Daily" {
		t.Fatalf("%+v", ev)
	}
}

func TestParseInvite(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VTIMEZONE\r\nTZID:W. Europe Standard Time\r\nEND:VTIMEZONE\r\nBEGIN:VEVENT\r\n" +
		"UID:abc@x.com\r\nSUMMARY:Budget\\, Q4\r\n review\r\nDTSTART;TZID=W. Europe Standard Time:20261020T100000\r\n" +
		"DTEND;TZID=W. Europe Standard Time:20261020T110000\r\nLOCATION:Room 1\r\nORGANIZER;CN=\"Ann B\":mailto:ann@x.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	inv := ParseInvite(ics)
	if inv == nil || inv.UID != "abc@x.com" || inv.Title != "Budget, Q4review" || inv.Method != "REQUEST" || inv.Location != "Room 1" ||
		inv.Organizer.Email != "ann@x.com" || inv.Organizer.Name != "Ann B" || !inv.Start.Equal(time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)) ||
		inv.End.Sub(inv.Start) != time.Hour {
		t.Fatalf("%+v", inv)
	}
	day := ParseInvite("BEGIN:VEVENT\nUID:d\nDTSTART;VALUE=DATE:20261224\nEND:VEVENT\n")
	if day == nil || !day.AllDay || day.End.Sub(day.Start) != 24*time.Hour || day.Method != "PUBLISH" {
		t.Fatalf("all-day %+v", day)
	}
	if ParseInvite("") != nil || ParseInvite("BEGIN:VEVENT\nSUMMARY:x\nEND:VEVENT") != nil {
		t.Fatal("garbage parsed")
	}
	built := ParseInvite(BuildInvite("u1", "A, B; C", "Here", models.Address{Name: "Me", Email: "me@x.com"},
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), time.Date(2026, 1, 2, 4, 4, 5, 0, time.UTC)))
	if built == nil || built.Title != "A, B; C" || built.UID != "u1" || built.Organizer.Email != "me@x.com" {
		t.Fatalf("round trip %+v", built)
	}
}

func TestAttendeeValidation(t *testing.T) {
	in := models.EventInput{Start: time.Now(), Attendees: []models.Attendee{{Email: " Ann@X.com ", Name: "Ann\r\nBcc: x"}, {Email: "ann@x.com"}}}
	if err := NormalizeInput(&in); err != nil || len(in.Attendees) != 1 || in.Attendees[0].Email != "ann@x.com" || in.Attendees[0].Name != "AnnBcc: x" {
		t.Fatalf("%+v %v", in.Attendees, err)
	}
	for _, bad := range []string{"not an email", "a@b.c\r\nBcc: x@y.z", "Ann <ann@x.com>", "a@b.c,d@e.f", ""} {
		in := models.EventInput{Start: time.Now(), Attendees: []models.Attendee{{Email: bad}}}
		if NormalizeInput(&in) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMeetingLinks(t *testing.T) {
	ev := FromInput(models.EventInput{Title: "x", MeetingURL: "https://zoom.us/j/123"})
	if ev.Location != "https://zoom.us/j/123" || ev.Description != "" || ev.MeetingURL != "https://zoom.us/j/123" {
		t.Fatalf("empty location: %+v", ev)
	}
	ev = FromInput(models.EventInput{Title: "x", Location: "Room 1", Description: "Agenda", MeetingURL: "https://zoom.us/j/123"})
	if ev.Location != "Room 1" || ev.Description != "Join: https://zoom.us/j/123\n\nAgenda" {
		t.Fatalf("with location: %+v", ev)
	}
	ev = FromInput(models.EventInput{Title: "x", Location: "Room 1", Description: "Join: https://zoom.us/j/123", MeetingURL: "https://zoom.us/j/123"})
	if strings.Count(ev.Description, "zoom.us") != 1 {
		t.Fatalf("duplicated link: %q", ev.Description)
	}
	for _, bad := range []string{"http://zoom.us/j/1", "javascript:alert(1)", "https://x y", "zoom.us/j/1"} {
		in := models.EventInput{Start: time.Now(), MeetingURL: bad}
		if NormalizeInput(&in) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for text, want := range map[string]string{
		"Join: https://us02web.zoom.us/j/8765?pwd=abc.":                                         "https://us02web.zoom.us/j/8765?pwd=abc",
		"Click https://teams.microsoft.com/l/meetup-join/19%3a, or dial":                        "https://teams.microsoft.com/l/meetup-join/19%3a",
		"(https://acme.webex.com/meet/bob)":                                                     "https://acme.webex.com/meet/bob",
		"https://meet.google.com/abc-defg-hij":                                                  "https://meet.google.com/abc-defg-hij",
		"see https://example.com/zoom.us/j/1 and http://zoom.us/j/2 or https://evilzoom.us/j/3": "",
	} {
		if got := DetectMeetingURL("", text); got != want {
			t.Errorf("DetectMeetingURL(%q) = %q, want %q", text, got, want)
		}
	}
	if DetectMeetingURL("https://zoom.us/j/1", "https://meet.google.com/x") != "https://zoom.us/j/1" {
		t.Fatal("location first")
	}
}

func TestStripMeetingURL(t *testing.T) {
	const u = "https://zoom.us/j/42"
	for _, c := range []struct{ loc, desc, wantLoc, wantDesc string }{
		{u, "Agenda", "", "Agenda"},
		{"Room 2", "Join: " + u + "\n\nAgenda", "Room 2", "Agenda"},
		{"Room 2, " + u, "See " + u + " today", "Room 2", "See  today"},
		{"Room 2", "Agenda", "Room 2", "Agenda"},
	} {
		l, d := StripMeetingURL(c.loc, c.desc, u)
		if l != c.wantLoc || d != c.wantDesc {
			t.Errorf("StripMeetingURL(%q, %q) = %q, %q", c.loc, c.desc, l, d)
		}
	}
	if !IsProviderConference("https://teams.microsoft.com/l/meetup-join/x") || IsProviderConference(u) {
		t.Fatal("IsProviderConference")
	}
}
