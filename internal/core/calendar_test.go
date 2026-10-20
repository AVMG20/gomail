package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
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
	if _, ok := findEvent(evs, "Vendor pitch (optional)"); ok {
		t.Fatal("declined invitation listed by default")
	}
	st := s.GetSettings()
	st.CalendarShowDeclined = true
	s.SaveSettings(st)
	withDeclined, _ := s.ListEvents(from, to)
	declined, ok := findEvent(withDeclined, "Vendor pitch (optional)")
	if !ok || declined.MyResponse != models.ResponseDeclined || declined.IsOrganizer || declined.CanEdit {
		t.Fatalf("declined %+v", declined)
	}
	if _, err := s.ListEvents(from, from.Add(401*24*time.Hour)); err == nil {
		t.Fatal("range over 400 days accepted")
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
	// All calendars of an account hidden: nothing listed, no calendar sync;
	// showing one again syncs right away.
	var outlookCals []models.Calendar
	for _, c := range cals {
		if c.AccountID == demo.OutlookAccountID {
			outlookCals = append(outlookCals, c)
			if err := s.SetCalendarVisible(c.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	evs3, _ := s.ListEvents(from, to)
	for _, ev := range evs3 {
		if ev.AccountID == demo.OutlookAccountID {
			t.Fatal("hidden account's events listed")
		}
	}
	if !s.allHidden(demo.OutlookAccountID) || s.allHidden(demo.GmailAccountID) {
		t.Fatal("allHidden")
	}
	var team models.Calendar
	for _, c := range outlookCals {
		if !c.Primary {
			team = c
		}
	}
	// Remove the team calendar's events locally; only a sync brings them back.
	teamRow, _ := s.store.Calendar(team.ID)
	_, _ = s.store.ApplyEvents(teamRow, store.EventChanges{Replace: true, Start: from.AddDate(-1, 0, 0), End: to.AddDate(1, 0, 0)})
	_ = s.syncCalendars(context.Background(), demo.OutlookAccountID) // skipped: all hidden
	if n := countCal(t, s, team.ID, from, to); n != 0 {
		t.Fatalf("hidden account synced: %d", n)
	}
	if err := s.SetCalendarVisible(team.ID, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countCal(t, s, team.ID, from, to) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("showing a calendar didn't sync the account")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, c := range outlookCals {
		_ = s.SetCalendarVisible(c.ID, true)
	}
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

func calByName(t *testing.T, s *Service, acct, name string) models.Calendar {
	t.Helper()
	cals, _ := s.ListCalendars()
	for _, c := range cals {
		if c.AccountID == acct && c.Name == name {
			return c
		}
	}
	t.Fatalf("no calendar %s/%s", acct, name)
	return models.Calendar{}
}

func TestDemoCalendarMoves(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	personal := calByName(t, s, demo.GmailAccountID, "Personal")
	work := calByName(t, s, demo.GmailAccountID, "Work")
	team := calByName(t, s, demo.OutlookAccountID, "Team")
	start := time.Now().Add(72 * time.Hour).Truncate(time.Hour)
	ev, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, CalendarID: personal.ID, Title: "Pottery", Start: start,
		Attendees: []models.Attendee{{Email: "sam@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	mc, err := s.MoveEventCheck(ev.ID, work.ID)
	if err != nil || !mc.Allowed || mc.CrossAccount || mc.HasAttendees || mc.Recurring {
		t.Fatalf("same-account check %+v %v", mc, err)
	}
	mc, _ = s.MoveEventCheck(ev.ID, team.ID)
	if !mc.Allowed || !mc.CrossAccount || !mc.HasAttendees {
		t.Fatalf("cross-account check %+v", mc)
	}
	if mc, _ := s.MoveEventCheck(ev.ID, personal.ID); mc.Allowed {
		t.Fatal("move to the same calendar allowed")
	}
	// Same account (native move), with a new title.
	moved, err := s.UpdateEvent(ev.ID, models.EventInput{CalendarID: work.ID, Title: "Pottery class", Start: start}, models.ScopeThis)
	if err != nil || moved.CalendarID != work.ID || moved.Title != "Pottery class" {
		t.Fatalf("moved %+v %v", moved, err)
	}
	if _, err := s.GetEvent(ev.ID); err == nil {
		t.Fatal("original still cached")
	}
	// Across accounts: recreated in Outlook, removed from Gmail.
	cross, err := s.UpdateEvent(moved.ID, models.EventInput{CalendarID: team.ID, Title: "Pottery class", Start: start}, models.ScopeThis)
	if err != nil || cross.AccountID != demo.OutlookAccountID || cross.CalendarID != team.ID {
		t.Fatalf("cross %+v %v", cross, err)
	}
	evs, _ := s.ListEvents(start.Add(-time.Hour), start.Add(2*time.Hour))
	n := 0
	for _, x := range evs {
		if x.Title == "Pottery class" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d copies after the move", n)
	}
	// Series: only as a whole.
	series, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, CalendarID: personal.ID, Title: "Yoga", Start: start,
		Recurrence: models.EventRecurrence{Frequency: models.RepeatDaily, Count: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateEvent(series.ID, models.EventInput{CalendarID: work.ID, Title: "Yoga", Start: start}, models.ScopeThis); err == nil {
		t.Fatal("moved a single occurrence")
	}
	if _, err := s.UpdateEvent(series.ID, models.EventInput{CalendarID: work.ID, Title: "Yoga", Start: start}, models.ScopeSeries); err != nil {
		t.Fatal(err)
	}
	evs, _ = s.ListEvents(start.Add(-time.Hour), start.AddDate(0, 0, 4))
	for _, x := range evs {
		if x.Title == "Yoga" && x.CalendarID != work.ID {
			t.Fatalf("occurrence left behind in %s", x.CalendarID)
		}
	}
	// Someone else's event can't be moved.
	all, _ := s.ListEvents(time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35))
	qbr, _ := findEvent(all, "Quarterly business review")
	if mc, _ := s.MoveEventCheck(qbr.ID, personal.ID); mc.Allowed {
		t.Fatal("invitation movable")
	}
}

func TestEventReminders(t *testing.T) {
	dir := t.TempDir()
	ui := &fakeUI{}
	s, err := New(Options{DataDir: dir, Demo: true, UI: ui, Secrets: secrets.NewMemory(), Online: func(context.Context) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCalendars(""); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.ListEvents(time.Now().Add(-time.Hour), time.Now().AddDate(0, 0, 3))
	var target models.CalendarEvent
	for _, ev := range evs {
		if ev.Title == "Sprint planning" && ev.AccountID == demo.GmailAccountID {
			target = ev
		}
	}
	if target.ID == "" {
		t.Fatal("no event")
	}
	old := nowFunc
	defer func() { nowFunc = old }()
	nowFunc = func() time.Time { return target.Start.Add(-8 * time.Minute) } // its 10-minute reminder fired 2 minutes ago
	s.checkReminders()
	s.checkReminders()
	count := func() (n int) {
		ui.mu.Lock()
		defer ui.mu.Unlock()
		for _, x := range ui.notified {
			if x == target.Title {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("%d reminders", count())
	}
	s.Close()
	// No repeat after a restart.
	s2, err := New(Options{DataDir: dir, Demo: true, UI: ui, Secrets: secrets.NewMemory(), Online: func(context.Context) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	_ = s2.SyncCalendars("")
	s2.checkReminders()
	if count() != 1 {
		t.Fatalf("%d reminders after restart", count())
	}
	// Too late (beyond the grace period): nothing.
	nowFunc = func() time.Time { return target.Start.Add(30 * time.Minute) }
	s2.checkReminders()
	if count() != 1 {
		t.Fatal("late reminder shown")
	}
	// Turned off: nothing at all.
	st := s2.GetSettings()
	st.CalendarNotifications = false
	s2.SaveSettings(st)
	ui.mu.Lock()
	before := len(ui.notified)
	ui.mu.Unlock()
	for _, ev := range evs {
		nowFunc = func() time.Time { return ev.Start.Add(-5 * time.Minute) }
		s2.checkReminders()
	}
	ui.mu.Lock()
	after := len(ui.notified)
	ui.mu.Unlock()
	if after != before {
		t.Fatal("reminders while turned off")
	}
	if title, body := ReminderText(models.CalendarEvent{Title: "X", Start: time.Now().Add(10 * time.Minute), Location: "Room"}, time.Now()); title != "X" ||
		!strings.HasPrefix(body, "In 10 minutes · ") || !strings.HasSuffix(body, " · Room") {
		t.Fatalf("%q %q", title, body)
	}
}

// failingCal wraps the demo calendar and fails writes on demand.
type failingCal struct {
	*demo.CalendarProvider
	fail error
}

func (f *failingCal) Update(ctx context.Context, cal calendar.Calendar, id string, ev calendar.Event, scope string) (calendar.Event, error) {
	if f.fail != nil {
		return calendar.Event{}, f.fail
	}
	return f.CalendarProvider.Update(ctx, cal, id, ev, scope)
}

func (f *failingCal) Delete(ctx context.Context, cal calendar.Calendar, id, scope, etag string) error {
	if f.fail != nil {
		return f.fail
	}
	return f.CalendarProvider.Delete(ctx, cal, id, scope, etag)
}

func (f *failingCal) Create(ctx context.Context, cal calendar.Calendar, ev calendar.Event) (calendar.Event, error) {
	if f.fail != nil {
		return calendar.Event{}, f.fail
	}
	return f.CalendarProvider.Create(ctx, cal, ev)
}

func TestCalendarOptimisticReverts(t *testing.T) {
	fc := map[string]*failingCal{}
	s, err := New(Options{DataDir: t.TempDir(), Demo: true, UI: &fakeUI{}, Secrets: secrets.NewMemory(),
		Online: func(context.Context) bool { return true },
		CalendarFactory: func(acct models.Account) (calendar.Provider, error) {
			f := &failingCal{CalendarProvider: demo.NewCalendar(acct)}
			fc[acct.ID] = f
			return f, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SyncCalendars(""); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.ListEvents(time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35))
	din, _ := findEvent(evs, "Dinner with Sam")
	fc[din.AccountID].fail = &httpx.Error{Service: "Google Calendar", Status: 500, Message: "backend error"}
	if _, err := s.UpdateEvent(din.ID, models.EventInput{Title: "Changed", Start: din.Start}, models.ScopeThis); err == nil ||
		!strings.Contains(err.Error(), "saving the event failed") {
		t.Fatalf("update error %v", err)
	}
	if got, _ := s.GetEvent(din.ID); got.Title != "Dinner with Sam" || !got.Start.Equal(din.Start) {
		t.Fatalf("not reverted %+v", got)
	}
	if err := s.DeleteEvent(din.ID, models.ScopeThis); err == nil {
		t.Fatal("delete should fail")
	}
	if _, err := s.GetEvent(din.ID); err != nil {
		t.Fatal("failed delete not reverted")
	}
	before, _ := s.ListEvents(time.Now(), time.Now().AddDate(0, 0, 10))
	if _, err := s.CreateEvent(models.EventInput{AccountID: din.AccountID, Title: "Doomed", Start: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("create should fail")
	}
	after, _ := s.ListEvents(time.Now(), time.Now().AddDate(0, 0, 10))
	if len(after) != len(before) {
		t.Fatal("failed create left a placeholder")
	}
	// Losing calendar access on a write withdraws consent.
	fc[din.AccountID].fail = calendar.ErrNoConsent
	if _, err := s.UpdateEvent(din.ID, models.EventInput{Title: "x", Start: din.Start}, models.ScopeThis); err == nil || !strings.Contains(err.Error(), "Enable calendar") {
		t.Fatalf("no consent %v", err)
	}
	if a, _ := s.store.Account(din.AccountID); a.CalendarConsent {
		t.Fatal("consent kept")
	}
}

func countCal(t *testing.T, s *Service, calID string, from, to time.Time) int {
	t.Helper()
	evs, err := s.ListEvents(from, to)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.CalendarID == calID {
			n++
		}
	}
	return n
}

func TestDemoVideoCalls(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	cals, _ := s.ListCalendars()
	for _, c := range cals {
		want := "meet"
		if c.AccountID == demo.OutlookAccountID {
			want = "teams"
		}
		if c.VideoCallProvider != want {
			t.Fatalf("%s provider %q", c.Name, c.VideoCallProvider)
		}
	}
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	ev, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Meet me", Start: start, AddVideoCall: true})
	if err != nil || !strings.HasPrefix(ev.MeetingURL, "https://meet.google.com/") {
		t.Fatalf("meet %+v %v", ev, err)
	}
	ev, err = s.CreateEvent(models.EventInput{AccountID: demo.OutlookAccountID, Title: "Teams", Start: start, AddVideoCall: true})
	if err != nil || !strings.HasPrefix(ev.MeetingURL, "https://teams.microsoft.com/") {
		t.Fatalf("teams %+v %v", ev, err)
	}
	// A typed link: in the location when that is empty, read back as the meeting URL.
	ev, err = s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Zoom", Start: start, MeetingURL: "https://zoom.us/j/42"})
	if err != nil || ev.MeetingURL != "https://zoom.us/j/42" || ev.Location != "https://zoom.us/j/42" {
		t.Fatalf("zoom %+v %v", ev, err)
	}
	up, err := s.UpdateEvent(ev.ID, models.EventInput{Title: "Zoom", Start: start, Location: "Room 2", MeetingURL: "https://zoom.us/j/42"}, models.ScopeThis)
	if err != nil || up.Location != "Room 2" || !strings.HasPrefix(up.Description, "Join: https://zoom.us/j/42") || up.MeetingURL != "https://zoom.us/j/42" {
		t.Fatalf("zoom update %+v %v", up, err)
	}
	if _, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "bad", Start: start, MeetingURL: "http://zoom.us/j/1"}); err == nil {
		t.Fatal("http link accepted")
	}
}
