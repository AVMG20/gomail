package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

func findEvent(evs []models.CalendarEvent, title string) (models.CalendarEvent, bool) {
	for _, e := range evs {
		if e.Title == title {
			return e, true
		}
	}
	return models.CalendarEvent{}, false
}

func TestDemoCalendarSyncAndViews(t *testing.T) {
	e := newEnv(t, "") // newEnv runs SyncNow, which syncs calendars too
	s := e.s
	cals, err := s.ListCalendars()
	if err != nil || len(cals) != 4 {
		t.Fatalf("calendars %d %v", len(cals), err)
	}
	from, to := time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35)
	evs, err := s.ListEvents(from, to)
	if err != nil || len(evs) < 40 {
		t.Fatalf("events %d %v", len(evs), err)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Start.Before(evs[i-1].Start) {
			t.Fatal("events not sorted by start")
		}
	}
	trip, ok := findEvent(evs, "Trip to Lisbon")
	if !ok || !trip.AllDay || trip.End.Sub(trip.Start) != 4*24*time.Hour || trip.Start.Hour() != 0 {
		t.Fatalf("all-day trip %+v", trip)
	}
	standup, ok := findEvent(evs, "Daily standup")
	if !ok || !standup.IsRecurring || standup.SeriesID == "" || standup.Recurrence != "Weekly on weekdays" || standup.MeetingURL == "" {
		t.Fatalf("standup %+v", standup)
	}
	declined, ok := findEvent(evs, "Vendor pitch (optional)")
	if !ok || declined.MyResponse != models.ResponseDeclined || declined.IsOrganizer || declined.CanEdit {
		t.Fatalf("declined %+v", declined)
	}
	review, _ := findEvent(evs, "Design review: inbox redesign")
	if !review.IsOrganizer || len(review.Attendees) < 2 || review.Organizer.Email == "" || len(review.Reminders) == 0 {
		t.Fatalf("meeting %+v", review)
	}

	// Hiding a calendar and turning an account off filter the view.
	var work models.Calendar
	for _, c := range cals {
		if c.AccountID == demo.GmailAccountID && c.Name == "Work" {
			work = c
		}
	}
	if err := s.SetCalendarVisible(work.ID, false); err != nil {
		t.Fatal(err)
	}
	evs2, _ := s.ListEvents(from, to)
	for _, ev := range evs2 {
		if ev.CalendarID == work.ID {
			t.Fatal("hidden calendar still listed")
		}
	}
	_ = s.SetCalendarVisible(work.ID, true)
	if err := s.SetAccountCalendarEnabled(demo.OutlookAccountID, false); err != nil {
		t.Fatal(err)
	}
	evs3, _ := s.ListEvents(from, to)
	for _, ev := range evs3 {
		if ev.AccountID == demo.OutlookAccountID {
			t.Fatal("disabled account's events listed")
		}
	}
	if a, _ := s.store.Account(demo.OutlookAccountID); a.CalendarEnabled {
		t.Fatal("not stored")
	}
	if _, err := s.CreateEvent(models.EventInput{AccountID: demo.OutlookAccountID, Title: "x", Start: time.Now()}); err == nil {
		t.Fatal("created an event on a disabled calendar")
	}
	_ = s.SetAccountCalendarEnabled(demo.OutlookAccountID, true)
	if _, err := s.ListEvents(to, from); err == nil {
		t.Fatal("inverted range accepted")
	}
}

func TestDemoCalendarCRUDAndResponses(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	start := time.Now().Add(48 * time.Hour).Truncate(time.Hour)
	ev, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Planning", Start: start,
		TimeZone: "Europe/Amsterdam", Location: "Room 1", Attendees: []models.Attendee{{Email: "maya@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID == "" || ev.End.Sub(ev.Start) != time.Hour || !ev.IsOrganizer || ev.Title != "Planning" || !ev.CanEdit {
		t.Fatalf("created %+v", ev)
	}
	got, err := s.GetEvent(ev.ID)
	if err != nil || got.Location != "Room 1" || len(got.Attendees) < 2 {
		t.Fatalf("get %+v %v", got, err)
	}
	// Move it by an hour.
	in := models.EventInput{Title: "Planning (moved)", Start: start.Add(time.Hour), End: start.Add(2 * time.Hour), TimeZone: "Europe/Amsterdam"}
	up, err := s.UpdateEvent(ev.ID, in, models.ScopeThis)
	if err != nil || up.Title != "Planning (moved)" || !up.Start.Equal(start.Add(time.Hour)) {
		t.Fatalf("updated %+v %v", up, err)
	}
	if err := s.DeleteEvent(ev.ID, models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEvent(ev.ID); err == nil {
		t.Fatal("deleted event still there")
	}

	// A weekly series of three.
	series, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Piano lesson", Start: start, AllDay: false,
		Recurrence: models.EventRecurrence{Frequency: models.RepeatWeekly, Count: 3}})
	if err != nil || !series.IsRecurring || series.SeriesID == "" {
		t.Fatalf("series %+v %v", series, err)
	}
	occ := func() []models.CalendarEvent {
		evs, _ := s.ListEvents(start.Add(-time.Hour), start.AddDate(0, 0, 30))
		var out []models.CalendarEvent
		for _, e := range evs {
			if e.SeriesID == series.SeriesID {
				out = append(out, e)
			}
		}
		return out
	}
	if n := len(occ()); n != 3 {
		t.Fatalf("%d occurrences", n)
	}
	if !strings.HasPrefix(occ()[0].Recurrence, "Weekly on") || !strings.Contains(occ()[0].RRule, "COUNT=3") {
		t.Fatalf("recurrence %q %q", occ()[0].Recurrence, occ()[0].RRule)
	}
	// Shift the whole series 30 minutes later.
	first := occ()[0]
	if _, err := s.UpdateEvent(first.ID, models.EventInput{Title: "Piano", Start: first.Start.Add(30 * time.Minute), End: first.End.Add(30 * time.Minute)},
		models.ScopeSeries); err != nil {
		t.Fatal(err)
	}
	for _, o := range occ() {
		if o.Title != "Piano" || o.Start.Minute() != 30 {
			t.Fatalf("series not updated: %+v", o)
		}
	}
	// Delete one occurrence, then the series.
	if err := s.DeleteEvent(occ()[1].ID, models.ScopeThis); err != nil || len(occ()) != 2 {
		t.Fatalf("delete one: %v %d", err, len(occ()))
	}
	if err := s.DeleteEvent(occ()[0].ID, models.ScopeSeries); err != nil || len(occ()) != 0 {
		t.Fatalf("delete series: %v %d", err, len(occ()))
	}

	// Answer an invitation; organizers can't.
	evs, _ := s.ListEvents(time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35))
	var qbr models.CalendarEvent
	for _, e := range evs {
		if e.Title == "Quarterly business review" && e.AccountID == demo.GmailAccountID {
			qbr = e
		}
	}
	if qbr.MyResponse != models.ResponseNone {
		t.Fatalf("qbr %+v", qbr)
	}
	if err := s.RespondToEvent(qbr.ID, models.ResponseAccepted); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetEvent(qbr.ID); got.MyResponse != models.ResponseAccepted {
		t.Fatalf("response %q", got.MyResponse)
	}
	s.SyncCalendars(demo.GmailAccountID) // the provider kept the answer
	if got, _ := s.GetEvent(qbr.ID); got.MyResponse != models.ResponseAccepted {
		t.Fatalf("response after sync %q", got.MyResponse)
	}
	design, _ := findEvent(evs, "Design review: inbox redesign")
	if err := s.RespondToEvent(design.ID, models.ResponseDeclined); err == nil {
		t.Fatal("organizer responded")
	}
	if err := s.RespondToEvent(qbr.ID, "maybe"); err == nil {
		t.Fatal("bad response accepted")
	}
	if _, err := s.UpdateEvent(qbr.ID, models.EventInput{Title: "mine now", Start: qbr.Start}, models.ScopeThis); err == nil {
		t.Fatal("edited someone else's event")
	}

	// The demo invitation email links to the synced event.
	res, _ := s.SearchMessages(models.SearchQuery{Query: "Invitation: Quarterly business review", AccountID: demo.GmailAccountID})
	if len(res) == 0 {
		t.Fatal("invite mail not found")
	}
	m, err := s.Message(res[0].Message.ID)
	if err != nil || m.Invite == nil || m.Invite.UID == "" || m.Invite.EventID != qbr.ID || m.Invite.Method != "REQUEST" ||
		!m.Invite.Start.Equal(qbr.Start) {
		t.Fatalf("invite %+v %v (event %s %v)", m.Invite, err, qbr.ID, qbr.Start)
	}
}

func TestCalendarInputValidation(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	for _, in := range []models.EventInput{
		{AccountID: demo.GmailAccountID, Title: "no start"},
		{AccountID: demo.GmailAccountID, Title: "backwards", Start: time.Now(), End: time.Now().Add(-time.Hour)},
		{AccountID: demo.GmailAccountID, Title: "bad repeat", Start: time.Now(), Recurrence: models.EventRecurrence{Frequency: "hourly"}},
		{AccountID: "nope", Title: "no account", Start: time.Now()},
	} {
		if _, err := s.CreateEvent(in); err == nil {
			t.Errorf("accepted %+v", in.Title)
		}
	}
	// All-day defaults: one day, dates at 00:00 UTC.
	ev, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Holiday", AllDay: true,
		Start: time.Date(2026, 12, 24, 15, 0, 0, 0, time.UTC)})
	if err != nil || !ev.AllDay || !ev.Start.Equal(time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC)) || !ev.End.Equal(ev.Start.AddDate(0, 0, 1)) {
		t.Fatalf("all-day %+v %v", ev, err)
	}
}
