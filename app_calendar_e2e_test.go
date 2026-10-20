package main

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/models"
)

// TestEndToEndCalendar runs the calendar features through MailService
// against the fake Google Calendar and Graph servers.
func TestEndToEndCalendar(t *testing.T) {
	e := newE2E(t)
	m := e.m
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(48 * time.Hour)
	gMeet := e.gcal.Add(fakes.CalEvent{Title: "Google sync", Start: day.Add(9 * time.Hour), End: day.Add(10 * time.Hour)})
	e.gcal.Add(fakes.CalEvent{Title: "Holiday", AllDay: true, Start: day.AddDate(0, 0, 1), End: day.AddDate(0, 0, 2)})
	gInvite := e.gcal.Add(fakes.CalEvent{Title: "Google invite", Start: day.Add(13 * time.Hour), End: day.Add(14 * time.Hour),
		Attendee: "boss@example.com", Response: "needsAction"})
	oInvite := e.graph.AddCalEvent(fakes.CalEvent{Title: "Outlook invite", Start: day.Add(15 * time.Hour), End: day.Add(16 * time.Hour),
		Attendee: "carol@contoso.example", Response: "none"})
	e.graph.AddCalEvent(fakes.CalEvent{Title: "Outlook own", Start: day.Add(17 * time.Hour), End: day.Add(18 * time.Hour)})
	e.graph.Deliver(fakes.GraphMessage{Folder: "inbox", Subject: "Invitation: Outlook invite", From: "carol@contoso.example",
		To: []string{"bob@contoso.example"}, HTML: "<p>Please join</p>", MeetingEventID: oInvite})

	st, _ := m.GetSettings()
	st.GoogleClientID, st.GoogleClientSecret, st.MicrosoftClientID = "g", "gs", "m"
	if err := m.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	// The Gmail sign-in leaves calendar access unticked (like an account
	// added before calendar support).
	e.oauth.Grant = func(scope string) string { return strings.Replace(scope, auth.GoogleCalendarScope, "", 1) }
	ga, err := m.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	e.oauth.Grant = nil
	oa, err := m.AddAccount(models.ProviderOutlook)
	if err != nil {
		t.Fatal(err)
	}
	if ga.CalendarConsent || !oa.CalendarConsent {
		t.Fatalf("consent gmail=%v outlook=%v", ga.CalendarConsent, oa.CalendarConsent)
	}
	if err := m.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	from, to := day.AddDate(0, 0, -1), day.AddDate(0, 0, 5)
	titles := func() map[string]models.CalendarEvent {
		evs, err := m.ListEvents(from, to)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]models.CalendarEvent{}
		for _, ev := range evs {
			out[ev.Title] = ev
		}
		return out
	}
	if got := titles(); len(got) != 2 || got["Outlook invite"].ID == "" {
		t.Fatalf("before enabling Google: %v", got)
	}
	if err := m.SyncCalendars(ga.ID); err == nil || !strings.Contains(err.Error(), "Enable calendar") {
		t.Fatalf("sync without consent: %v", err)
	}

	// Enable the Google calendar: incremental consent, mail kept.
	ga, err = m.EnableCalendar(ga.ID)
	if err != nil || !ga.CalendarConsent {
		t.Fatalf("enable: %+v %v", ga, err)
	}
	if err := m.SyncCalendars(""); err != nil {
		t.Fatal(err)
	}
	cals, _ := m.ListCalendars()
	if len(cals) != 2 {
		t.Fatalf("calendars %+v", cals)
	}
	got := titles()
	if len(got) != 5 || !got["Holiday"].AllDay || got["Google sync"].ID == "" || !got["Outlook own"].IsOrganizer {
		t.Fatalf("all events: %v", got)
	}
	if ev := got["Outlook own"]; ev.TimeZone != "Europe/Berlin" || !ev.Start.Equal(day.Add(17*time.Hour)) {
		t.Fatalf("outlook tz %+v", ev)
	}

	// Create, update and delete on Google.
	created, err := m.CreateEvent(models.EventInput{AccountID: ga.ID, Title: "Lunch", Start: day.Add(12 * time.Hour), TimeZone: "Europe/Amsterdam"})
	if err != nil || created.Title != "Lunch" {
		t.Fatalf("create %+v %v", created, err)
	}
	updated, err := m.UpdateEvent(created.ID, models.EventInput{Title: "Long lunch", Start: day.Add(12 * time.Hour), End: day.Add(14 * time.Hour)}, "this")
	if err != nil || updated.Title != "Long lunch" || updated.End.Sub(updated.Start) != 2*time.Hour {
		t.Fatalf("update %+v %v", updated, err)
	}
	if err := m.DeleteEvent(created.ID, "this"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetEvent(created.ID); err == nil {
		t.Fatal("deleted event still cached")
	}
	if _, ok := e.gcal.Event(gMeet); !ok {
		t.Fatal("unrelated event lost")
	}
	// Create on Outlook.
	if _, err := m.CreateEvent(models.EventInput{AccountID: oa.ID, Title: "Teams sync", Start: day.Add(8 * time.Hour), TimeZone: "Europe/Amsterdam"}); err != nil {
		t.Fatal(err)
	}

	// An Outlook meeting request links to its calendar event.
	got = titles()
	inviteMsg := e.msg(oa.ID, "Invitation: Outlook invite")
	full, err := m.GetMessage(inviteMsg.ID)
	if err != nil || full.Invite == nil || full.Invite.Method != "REQUEST" || full.Invite.EventID != got["Outlook invite"].ID ||
		!full.Invite.Start.Equal(day.Add(15*time.Hour)) || full.Invite.Title != "Outlook invite" {
		t.Fatalf("outlook invite %+v %v", full.Invite, err)
	}

	// A change made elsewhere isn't overwritten (If-Match → 412).
	gs := got["Google sync"]
	e.gcal.Touch(gMeet, "Renamed elsewhere")
	if _, err := m.UpdateEvent(gs.ID, models.EventInput{Title: "Mine", Start: gs.Start, End: gs.End}, "this"); err == nil ||
		!strings.Contains(err.Error(), "changed elsewhere") {
		t.Fatalf("conflict: %v", err)
	}
	if ev, _ := m.GetEvent(gs.ID); ev.Title != "Renamed elsewhere" {
		t.Fatalf("after conflict %q", ev.Title)
	}
	if ev, _ := e.gcal.Event(gMeet); ev.Title != "Renamed elsewhere" {
		t.Fatalf("server event clobbered: %q", ev.Title)
	}
	if _, err := m.UpdateEvent(gs.ID, models.EventInput{Title: "Mine", Start: gs.Start, End: gs.End}, "this"); err != nil {
		t.Fatalf("retry after conflict: %v", err)
	}

	// Answer invitations on both providers.
	got = titles()
	if err := m.RespondToEvent(got["Google invite"].ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	if ev, _ := e.gcal.Event(gInvite); ev.Response != "accepted" {
		t.Fatalf("google response %q", ev.Response)
	}
	if err := m.RespondToEvent(got["Outlook invite"].ID, "declined"); err != nil {
		t.Fatal(err)
	}
	if ev, _ := e.graph.CalEvent(oInvite); ev.Response != "declined" {
		t.Fatalf("outlook response %q", ev.Response)
	}
	if _, missing := e.graph.Stats(); missing != 0 {
		t.Fatalf("%d Graph requests without immutable ids", missing)
	}

	// Insufficient scope withdraws consent and hides the account's events.
	e.gcal.SetNoScope(true)
	if err := m.SyncCalendars(ga.ID); err == nil {
		t.Fatal("sync succeeded without scope")
	}
	accts, _ := m.GetAccounts()
	for _, a := range accts {
		if a.ID == ga.ID && a.CalendarConsent {
			t.Fatal("consent not withdrawn")
		}
	}
	for title, ev := range titles() {
		if ev.AccountID == ga.ID {
			t.Fatalf("event %q of an account without consent listed", title)
		}
	}
	// Hiding every calendar hides everything.
	cals, _ = m.ListCalendars()
	for _, c := range cals {
		if err := m.SetCalendarVisible(c.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(titles()); n != 0 {
		t.Fatalf("%d events with every calendar off", n)
	}
}
