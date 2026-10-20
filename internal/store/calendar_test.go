package store

import (
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/models"
)

func TestCalendarStorePermissionsAndDeletion(t *testing.T) {
	s := openTest(t)
	if err := s.InsertAccount(models.Account{ID: "a1", Provider: models.ProviderGmail, Email: "a@x.com", CalendarConsent: true, CalendarEnabled: true}); err != nil {
		t.Fatal(err)
	}
	cals := []calendar.Calendar{{RemoteID: "main", Name: "Main", Primary: true, CanEdit: true}, {RemoteID: "shared", Name: "Shared", CanEdit: true}}
	if err := s.ReplaceCalendars("a1", cals); err != nil {
		t.Fatal(err)
	}
	shared, _ := s.Calendar(CalendarID("a1", "shared"))
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	ids, err := s.ApplyEvents(shared, EventChanges{Upserts: []calendar.Event{
		{RemoteID: "e1", Title: "One", Start: day.Add(9 * time.Hour), End: day.Add(10 * time.Hour), CanEdit: true, ETag: `"1"`, Status: models.EventConfirmed},
		{RemoteID: "e2", Title: "Declined", Start: day.Add(11 * time.Hour), End: day.Add(12 * time.Hour), MyResponse: models.ResponseDeclined, Status: models.EventConfirmed},
	}})
	if err != nil || len(ids) != 2 {
		t.Fatal(err)
	}
	ev, _ := s.Event(ids[0])
	if !ev.CanEdit || ev.ETag != `"1"` {
		t.Fatalf("%+v", ev)
	}
	if evs, _ := s.Events(day, day.AddDate(0, 0, 1), false); len(evs) != 1 {
		t.Fatalf("declined filter: %d", len(evs))
	}
	if evs, _ := s.Events(day, day.AddDate(0, 0, 1), true); len(evs) != 2 {
		t.Fatalf("with declined: %d", len(evs))
	}
	// Permission downgrade: the calendar becomes read-only.
	cals[1].CanEdit = false
	s.ReplaceCalendars("a1", cals)
	if ev, _ := s.Event(ids[0]); ev.CanEdit {
		t.Fatal("event still editable on a read-only calendar")
	}
	// A full window fetch drops events that disappeared.
	shared, _ = s.Calendar(shared.ID)
	s.ApplyEvents(shared, EventChanges{Upserts: []calendar.Event{{RemoteID: "e1", Title: "One", Start: day.Add(9 * time.Hour), End: day.Add(10 * time.Hour)}},
		Replace: true, Start: day, End: day.AddDate(0, 0, 1)})
	if _, err := s.Event(ids[1]); err == nil {
		t.Fatal("event outside the full fetch kept")
	}
	// Deleted calendar: its events go too.
	s.ReplaceCalendars("a1", cals[:1])
	if _, err := s.Event(ids[0]); err == nil {
		t.Fatal("events of a deleted calendar kept")
	}
	// Turning the account off hides everything.
	main, _ := s.Calendar(CalendarID("a1", "main"))
	s.ApplyEvents(main, EventChanges{Upserts: []calendar.Event{{RemoteID: "m1", Start: day, End: day.Add(time.Hour)}}})
	s.SetCalendarEnabled("a1", false)
	if evs, _ := s.Events(day, day.AddDate(0, 0, 1), true); len(evs) != 0 {
		t.Fatal("disabled account listed")
	}
}
