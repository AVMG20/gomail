package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
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

// countingCal counts calendar syncs.
type countingCal struct {
	*demo.CalendarProvider
	mu    sync.Mutex
	syncs int
}

func (c *countingCal) Calendars(ctx context.Context) ([]calendar.Calendar, error) {
	c.mu.Lock()
	c.syncs++
	c.mu.Unlock()
	return c.CalendarProvider.Calendars(ctx)
}

func (c *countingCal) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.syncs
}

func TestCalendarDisableMuteAndViewFilters(t *testing.T) {
	provs := map[string]*countingCal{}
	var mu sync.Mutex
	ui := &fakeUI{}
	s, err := New(Options{DataDir: t.TempDir(), Demo: true, UI: ui, Secrets: secrets.NewMemory(),
		Online: func(context.Context) bool { return true },
		CalendarFactory: func(acct models.Account) (calendar.Provider, error) {
			mu.Lock()
			defer mu.Unlock()
			p := &countingCal{CalendarProvider: demo.NewCalendar(acct)}
			provs[acct.ID] = p
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SyncCalendars(""); err != nil {
		t.Fatal(err)
	}
	from, to := time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35)
	evs, _ := s.ListEvents(from, to)
	var gmailEv, outlookEv models.CalendarEvent
	for _, ev := range evs {
		if ev.Title == "Sprint planning" {
			if ev.AccountID == demo.GmailAccountID {
				gmailEv = ev
			} else {
				outlookEv = ev
			}
		}
	}
	notified := func(title string) int {
		ui.mu.Lock()
		defer ui.mu.Unlock()
		n := 0
		for _, x := range ui.notified {
			if x == title {
				n++
			}
		}
		return n
	}
	old := nowFunc
	remindAt := func(ev models.CalendarEvent) {
		nowFunc = func() time.Time { return ev.Start.Add(-9 * time.Minute) }
		s.checkReminders()
	}

	// Hidden calendar: a view filter only. Events stay synced and remind.
	work := calByName(t, s, demo.GmailAccountID, "Work")
	if err := s.SetCalendarVisible(work.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countCal(t, s, work.ID, from, to); n != 0 {
		t.Fatalf("hidden calendar listed (%d)", n)
	}
	before := provs[demo.GmailAccountID].count()
	_ = s.SyncCalendars(demo.GmailAccountID)
	if provs[demo.GmailAccountID].count() == before {
		t.Fatal("hidden calendars not synced")
	}
	remindAt(gmailEv)
	if notified(gmailEv.Title) != 1 {
		t.Fatal("no reminder for a hidden calendar")
	}
	_ = s.SetCalendarVisible(work.ID, true)

	// Muted: events sync and show, no reminders.
	if err := s.SetAccountCalendarMuted(demo.OutlookAccountID, true); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.store.Account(demo.OutlookAccountID); !a.CalendarMuted || !a.CalendarEnabled {
		t.Fatalf("muted flags %+v", a)
	}
	before = provs[demo.OutlookAccountID].count()
	_ = s.SyncCalendars(demo.OutlookAccountID)
	if provs[demo.OutlookAccountID].count() == before {
		t.Fatal("muted account not synced")
	}
	if _, ok := findEvent(func() []models.CalendarEvent { e, _ := s.ListEvents(from, to); return e }(), "Daily standup"); !ok {
		t.Fatal("events hidden")
	}
	n := notified(outlookEv.Title)
	remindAt(outlookEv)
	if notified(outlookEv.Title) != n {
		t.Fatal("reminder for a muted account")
	}
	_ = s.SetAccountCalendarMuted(demo.OutlookAccountID, false)

	// Disabled: no sync, no reminders, not listed; calendars still listed.
	if err := s.SetAccountCalendarEnabled(demo.OutlookAccountID, false); err != nil {
		t.Fatal(err)
	}
	before = provs[demo.OutlookAccountID].count()
	_ = s.syncCalendars(context.Background(), demo.OutlookAccountID)
	s.engine.SyncNow(context.Background(), demo.OutlookAccountID)
	if provs[demo.OutlookAccountID].count() != before {
		t.Fatal("disabled account synced")
	}
	for _, ev := range func() []models.CalendarEvent { e, _ := s.ListEvents(from, to); return e }() {
		if ev.AccountID == demo.OutlookAccountID {
			t.Fatal("disabled account's events listed")
		}
	}
	cals, _ := s.ListCalendars()
	listed := 0
	for _, c := range cals {
		if c.AccountID == demo.OutlookAccountID {
			listed++
		}
	}
	if listed != 2 {
		t.Fatalf("%d calendars of the disabled account listed", listed)
	}
	if err := s.SyncCalendars(demo.OutlookAccountID); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("explicit sync of a disabled account: %v", err)
	}
	nowFunc = func() time.Time { return outlookEv.Start.Add(-9 * time.Minute) }
	s.store.SetSetting(remindedKey, map[string]int64{})
	s.checkReminders()
	if notified(outlookEv.Title) != n {
		t.Fatal("reminder for a disabled account")
	}
	if _, err := s.CreateEvent(models.EventInput{AccountID: demo.OutlookAccountID, Title: "x", Start: time.Now()}); err == nil {
		t.Fatal("created an event in a disabled calendar")
	}
	// Re-enabling syncs right away (in the background: restore the clock
	// first).
	nowFunc = old
	if err := s.SetAccountCalendarEnabled(demo.OutlookAccountID, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for provs[demo.OutlookAccountID].count() == before {
		if time.Now().After(deadline) {
			t.Fatal("re-enabling didn't sync")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Clearing the meeting link removes it: a typed link leaves the location and
// description, a Meet conference is detached, a Teams meeting is refused.
func TestRemoveMeetingLink(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	zoom, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Zoom", Start: start, Location: "Room 2", Description: "Agenda", MeetingURL: "https://zoom.us/j/42"})
	if err != nil || zoom.MeetingURL == "" {
		t.Fatalf("%+v %v", zoom, err)
	}
	up, err := s.UpdateEvent(zoom.ID, models.EventInput{Title: "Zoom", Start: start, Location: zoom.Location, Description: zoom.Description, RemoveMeetingURL: true}, models.ScopeThis)
	if err != nil || up.MeetingURL != "" || up.Location != "Room 2" || up.Description != "Agenda" {
		t.Fatalf("typed link not removed: %+v %v", up, err)
	}
	meet, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Meet", Start: start, AddVideoCall: true})
	if err != nil || meet.MeetingURL == "" {
		t.Fatalf("%+v %v", meet, err)
	}
	up, err = s.UpdateEvent(meet.ID, models.EventInput{Title: "Meet", Start: start, RemoveMeetingURL: true}, models.ScopeThis)
	if err != nil || up.MeetingURL != "" {
		t.Fatalf("meet not removed: %+v %v", up, err)
	}
	// Replacing a link: the old one goes, the new one is added.
	z7, err := s.CreateEvent(models.EventInput{AccountID: demo.GmailAccountID, Title: "Z7", Start: start, MeetingURL: "https://zoom.us/j/7"})
	if err != nil || z7.Location != "https://zoom.us/j/7" {
		t.Fatalf("%+v %v", z7, err)
	}
	up, err = s.UpdateEvent(z7.ID, models.EventInput{Title: "Z7", Start: start, Location: z7.Location, RemoveMeetingURL: true, MeetingURL: "https://webex.com/m/9"}, models.ScopeThis)
	if err != nil || up.MeetingURL != "https://webex.com/m/9" || up.Location != "https://webex.com/m/9" || strings.Contains(up.Description, "zoom") {
		t.Fatalf("replace %+v %v", up, err)
	}
	teams, err := s.CreateEvent(models.EventInput{AccountID: demo.OutlookAccountID, Title: "Teams", Start: start, AddVideoCall: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateEvent(teams.ID, models.EventInput{Title: "Teams", Start: start, RemoveMeetingURL: true}, models.ScopeThis); err == nil || !strings.Contains(err.Error(), "Teams") {
		t.Fatalf("teams removal: %v", err)
	}
	if got, _ := s.GetEvent(teams.ID); got.MeetingURL == "" {
		t.Fatal("refused removal still changed the event")
	}
}
