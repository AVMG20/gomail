package demo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/models"
)

// CalendarProvider is an in-memory calendar.Provider with realistic events
// around today: meetings with attendees and meeting links, all-day events,
// a multi-day trip, a weekly standup and a declined invitation.
type CalendarProvider struct {
	mu      sync.Mutex
	me      models.Address
	cals    []calendar.Calendar
	events  map[string]map[string]*calendar.Event // calendar -> id -> event
	seq     int
	Latency time.Duration
}

// NewCalendar creates the demo calendars of an account from Accounts().
func NewCalendar(acct models.Account) *CalendarProvider {
	p := &CalendarProvider{me: models.Address{Name: "Alex Demo", Email: acct.Email}, events: map[string]map[string]*calendar.Event{}}
	tz := demoTZ(acct.Provider)
	names := [2]string{"Personal", "Work"}
	colors := [2]string{"#16a34a", "#2563eb"}
	video := "meet"
	if acct.Provider == models.ProviderOutlook {
		names, colors, video = [2]string{"Calendar", "Team"}, [2]string{"#0078d4", "#c239b3"}, "teams"
	}
	for i, n := range names {
		c := calendar.Calendar{RemoteID: strings.ToLower(n), Name: n, Color: colors[i], Primary: i == 0, CanEdit: true,
			TimeZone: tz, DefaultReminders: []int{10}, VideoCallProvider: video}
		p.cals = append(p.cals, c)
		p.events[c.RemoteID] = map[string]*calendar.Event{}
	}
	p.seed(time.Now(), tz, acct.Provider == models.ProviderOutlook)
	return p
}

func (p *CalendarProvider) sleep(ctx context.Context) error {
	if p.Latency <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.Latency):
		return nil
	}
}

// videoLink fakes the conference link the providers would create.
func (p *CalendarProvider) videoLink(cal calendar.Calendar, id string) string {
	if cal.VideoCallProvider == "teams" {
		return "https://teams.microsoft.com/l/meetup-join/demo-" + id
	}
	return "https://meet.google.com/demo-" + id
}

// meeting fills the meeting link of a created/updated event.
func (p *CalendarProvider) meeting(cal calendar.Calendar, ev *calendar.Event, req calendar.Event) {
	switch {
	case req.MeetingURL != "":
		ev.MeetingURL = req.MeetingURL
	case req.AddVideoCall && ev.MeetingURL == "" && cal.VideoCallProvider != "":
		ev.MeetingURL = p.videoLink(cal, ev.RemoteID)
	case ev.MeetingURL == "":
		ev.MeetingURL = calendar.DetectMeetingURL(ev.Location, ev.Description)
	}
}

func (p *CalendarProvider) nextID() string {
	p.seq++
	return "ev" + strconv.Itoa(p.seq)
}

type seedEvent struct {
	cal        int
	day        int // relative to today
	hour, min  int
	minutes    int
	days       int // all-day length in days (0 = timed)
	title      string
	location   string
	desc       string
	attendees  []string
	meet       bool
	organizer  string // "" = me
	myResponse string
	uid        string
}

// demoTZ is the home zone of a demo account.
func demoTZ(provider string) string {
	if provider == models.ProviderOutlook {
		return "America/New_York"
	}
	return "Europe/Amsterdam"
}

// Invitation shown in the demo mailbox (matches the QBR event).
const qbrUID = "qbr-review@demo.gomail"

// qbrTimes returns the start and end of the demo QBR meeting.
func qbrTimes(now time.Time, provider string) (time.Time, time.Time) {
	loc, err := time.LoadLocation(demoTZ(provider))
	if err != nil {
		loc = time.UTC
	}
	n := now.In(loc)
	d := time.Date(n.Year(), n.Month(), n.Day(), 11, 0, 0, 0, loc).AddDate(0, 0, 2)
	return d.UTC(), d.Add(time.Hour).UTC()
}

func (p *CalendarProvider) seed(now time.Time, tz string, outlook bool) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	meet := "https://meet.google.com/abc-defg-hij"
	if outlook {
		meet = "https://teams.microsoft.com/l/meetup-join/demo"
	}
	work := []seedEvent{
		{cal: 1, day: 0, hour: 10, minutes: 30, title: "Design review: inbox redesign", location: "Room 4.12", attendees: []string{"maya.chen@northwind.io", "lars.jensen@northwind.io"}, meet: true, desc: "Walk through the new conversation view and the label sidebar."},
		{cal: 1, day: 0, hour: 14, minutes: 60, title: "1:1 with Priya", attendees: []string{"priya.raman@northwind.io"}, meet: true, organizer: "priya.raman@northwind.io", myResponse: models.ResponseAccepted},
		{cal: 1, day: 1, hour: 9, min: 30, minutes: 45, title: "Sprint planning", attendees: []string{"maya.chen@northwind.io", "lars.jensen@northwind.io", "priya.raman@northwind.io"}, meet: true},
		{cal: 1, day: 1, hour: 16, minutes: 30, title: "Customer call: Acme Corp", attendees: []string{"jordan@acme.example"}, meet: true, organizer: "jordan@acme.example", myResponse: models.ResponseTentative, desc: "Renewal and the Q4 roadmap."},
		{cal: 1, day: 2, hour: 11, minutes: 60, title: "Quarterly business review", location: "Board room", attendees: []string{"cfo@northwind.io", "ceo@northwind.io"}, organizer: "ceo@northwind.io", myResponse: models.ResponseNone, uid: qbrUID},
		{cal: 1, day: 3, hour: 13, minutes: 60, title: "Vendor pitch (optional)", attendees: []string{"sales@vendor.example"}, organizer: "sales@vendor.example", myResponse: models.ResponseDeclined, meet: true},
		{cal: 1, day: 4, hour: 15, minutes: 90, title: "Architecture deep dive", attendees: []string{"lars.jensen@northwind.io"}, meet: true},
		{cal: 1, day: -1, hour: 10, minutes: 60, title: "Retrospective", attendees: []string{"maya.chen@northwind.io", "lars.jensen@northwind.io"}, meet: true},
		{cal: 1, day: -2, hour: 15, minutes: 30, title: "Interview: senior engineer", attendees: []string{"recruiting@northwind.io"}, organizer: "recruiting@northwind.io", myResponse: models.ResponseAccepted, meet: true},
		{cal: 1, day: 7, hour: 10, minutes: 120, title: "Security training", location: "Online", organizer: "it@northwind.io", myResponse: models.ResponseNone, meet: true},
		{cal: 1, day: 9, hour: 14, minutes: 60, title: "Release go/no-go", attendees: []string{"maya.chen@northwind.io", "priya.raman@northwind.io"}, meet: true},
		{cal: 1, day: 12, days: 1, title: "Company offsite", location: "Utrecht"},
		{cal: 1, day: 16, hour: 11, minutes: 45, title: "Roadmap sync", attendees: []string{"ceo@northwind.io"}, meet: true},
		{cal: 1, day: -8, hour: 9, minutes: 60, title: "Kickoff: calendar project", attendees: []string{"maya.chen@northwind.io"}, meet: true},
		{cal: 1, day: 21, hour: 13, minutes: 60, title: "Budget review", organizer: "cfo@northwind.io", myResponse: models.ResponseNone},
	}
	personal := []seedEvent{
		{cal: 0, day: 0, hour: 19, minutes: 120, title: "Dinner with Sam", location: "Café de Jaren"},
		{cal: 0, day: 2, hour: 18, min: 30, minutes: 60, title: "Gym"},
		{cal: 0, day: 3, days: 1, title: "Mom's birthday 🎂"},
		{cal: 0, day: 5, days: 4, title: "Trip to Lisbon", location: "Lisbon, Portugal", desc: "Flight TP 663, hotel Bairro Alto."},
		{cal: 0, day: 6, hour: 12, minutes: 90, title: "Lunch with Sofia", location: "Time Out Market", attendees: []string{"sofia.martins@example.pt"}},
		{cal: 0, day: -3, hour: 8, minutes: 30, title: "Dentist", location: "Tandarts Centrum"},
		{cal: 0, day: 10, hour: 20, minutes: 150, title: "Concert", location: "Paradiso"},
		{cal: 0, day: 1, hour: 7, minutes: 45, title: "Morning run"},
		{cal: 0, day: 14, days: 1, title: "Public holiday"},
		{cal: 0, day: -5, hour: 17, minutes: 60, title: "Haircut"},
		{cal: 0, day: 18, hour: 10, minutes: 180, title: "Bike repair & errands"},
		{cal: 0, day: 25, days: 2, title: "Weekend in Ghent", location: "Ghent, Belgium"},
		{cal: 0, day: -12, hour: 19, minutes: 120, title: "Book club", attendees: []string{"sam@example.com", "noor@example.com"}},
		{cal: 0, day: 30, hour: 9, minutes: 60, title: "Car service", location: "Garage Noord"},
	}
	for _, s := range append(work, personal...) {
		c := p.cals[s.cal]
		ev := calendar.Event{RemoteID: p.nextID(), Title: s.title, Location: s.location, Description: s.desc, TimeZone: c.TimeZone,
			Status: models.EventConfirmed, Reminders: []int{10}, CanEdit: true, Updated: now.UTC()}
		ev.ICalUID = ev.RemoteID + "@demo.gomail"
		if s.uid != "" {
			ev.ICalUID = s.uid
		}
		day := today.AddDate(0, 0, s.day)
		if s.days > 0 {
			ev.AllDay = true
			ev.Start = calendar.DateUTC(day)
			ev.End = ev.Start.AddDate(0, 0, s.days)
		} else {
			ev.Start = time.Date(day.Year(), day.Month(), day.Day(), s.hour, s.min, 0, 0, loc).UTC()
			ev.End = ev.Start.Add(time.Duration(s.minutes) * time.Minute)
		}
		if s.meet {
			ev.MeetingURL = meet
		}
		p.people(&ev, s.organizer, s.myResponse, s.attendees)
		p.events[c.RemoteID][ev.RemoteID] = &ev
	}
	// Weekly standup (Mon–Fri) on the work calendar, ±10 weeks.
	c := p.cals[1]
	monday := today.AddDate(0, 0, -int((today.Weekday()+6)%7))
	series := calendar.Event{RemoteID: "standup", Title: "Daily standup", TimeZone: c.TimeZone, Status: models.EventConfirmed,
		Reminders: []int{5}, CanEdit: true, IsRecurring: true, RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", MeetingURL: meet}
	series.Recurrence = calendar.Summarize(series.RRule)
	series.ICalUID = "standup@demo.gomail"
	p.people(&series, "", "", []string{"maya.chen@northwind.io", "lars.jensen@northwind.io", "priya.raman@northwind.io"})
	for d := monday.AddDate(0, 0, -70); d.Before(monday.AddDate(0, 0, 70)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		occ := series
		occ.RemoteID = "standup_" + d.Format("20060102")
		occ.SeriesRemoteID = series.RemoteID
		occ.Start = time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc).UTC()
		occ.End = occ.Start.Add(15 * time.Minute)
		occ.Updated = now.UTC()
		p.events[c.RemoteID][occ.RemoteID] = &occ
	}
}

func (p *CalendarProvider) people(ev *calendar.Event, organizer, myResponse string, attendees []string) {
	ev.IsOrganizer = organizer == ""
	if ev.IsOrganizer {
		ev.Organizer, ev.MyResponse = p.me, models.ResponseOrganizer
	} else {
		ev.Organizer = models.Address{Name: nameOf(organizer), Email: organizer}
		ev.MyResponse = myResponse
		if ev.MyResponse == "" {
			ev.MyResponse = models.ResponseNone
		}
		ev.CanEdit = false
	}
	if len(attendees) == 0 && organizer == "" {
		return
	}
	ev.Attendees = []models.Attendee{{Name: ev.Organizer.Name, Email: ev.Organizer.Email, Response: models.ResponseOrganizer}}
	for i, a := range attendees {
		if a == organizer {
			continue
		}
		r := []string{models.ResponseAccepted, models.ResponseTentative, models.ResponseNone}[i%3]
		ev.Attendees = append(ev.Attendees, models.Attendee{Name: nameOf(a), Email: a, Response: r})
	}
	if !ev.IsOrganizer {
		ev.Attendees = append(ev.Attendees, models.Attendee{Name: p.me.Name, Email: p.me.Email, Response: ev.MyResponse})
	}
}

func nameOf(email string) string {
	local, _, _ := strings.Cut(email, "@")
	parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	for i, w := range parts {
		if w != "" {
			parts[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(parts, " ")
}

func (p *CalendarProvider) Calendars(ctx context.Context) ([]calendar.Calendar, error) {
	if err := p.sleep(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]calendar.Calendar(nil), p.cals...), nil
}

func (p *CalendarProvider) inRange(cal calendar.Calendar, start, end time.Time) []calendar.Event {
	var out []calendar.Event
	for _, ev := range p.events[cal.RemoteID] {
		if ev.Start.Before(end) && ev.End.After(start) {
			out = append(out, *ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Sync always returns the full window (the demo has no change log).
func (p *CalendarProvider) Sync(ctx context.Context, cal calendar.Calendar, req calendar.SyncRequest) (calendar.SyncResult, error) {
	if err := p.sleep(ctx); err != nil {
		return calendar.SyncResult{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return calendar.SyncResult{Upserts: p.inRange(cal, req.Start, req.End), Full: true}, nil
}

func (p *CalendarProvider) Range(ctx context.Context, cal calendar.Calendar, start, end time.Time) ([]calendar.Event, error) {
	if err := p.sleep(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inRange(cal, start, end), nil
}

func (p *CalendarProvider) store(cal calendar.Calendar) (map[string]*calendar.Event, error) {
	m, ok := p.events[cal.RemoteID]
	if !ok {
		return nil, errors.New("calendar not found")
	}
	return m, nil
}

// expand writes the occurrences of a series (at most 400, two years).
func expand(master calendar.Event, put func(calendar.Event)) {
	kv := map[string]string{}
	for _, part := range strings.Split(master.RRule, ";") {
		k, v, _ := strings.Cut(part, "=")
		kv[k] = v
	}
	count, _ := strconv.Atoi(kv["COUNT"])
	var until time.Time
	if u := kv["UNTIL"]; u != "" {
		until, _ = time.Parse("20060102T150405Z", u)
	}
	step := map[string][3]int{"DAILY": {0, 0, 1}, "WEEKLY": {0, 0, 7}, "MONTHLY": {0, 1, 0}, "YEARLY": {1, 0, 0}}[kv["FREQ"]]
	dur := master.End.Sub(master.Start)
	limit := master.Start.AddDate(2, 0, 0)
	for i := 0; i < 400; i++ {
		start := master.Start.AddDate(step[0]*i, step[1]*i, step[2]*i)
		if (count > 0 && i >= count) || (!until.IsZero() && start.After(until)) || start.After(limit) {
			return
		}
		occ := master
		occ.RemoteID = fmt.Sprintf("%s_%d", master.RemoteID, i)
		occ.SeriesRemoteID = master.RemoteID
		occ.Start, occ.End = start, start.Add(dur)
		put(occ)
	}
}

func (p *CalendarProvider) Create(ctx context.Context, cal calendar.Calendar, ev calendar.Event) (calendar.Event, error) {
	if err := p.sleep(ctx); err != nil {
		return calendar.Event{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.store(cal)
	if err != nil {
		return ev, err
	}
	ev.RemoteID = p.nextID()
	ev.ICalUID = ev.RemoteID + "@demo.gomail"
	ev.Updated = time.Now().UTC()
	ev.CanEdit = true
	if ev.Status == "" {
		ev.Status = models.EventConfirmed
	}
	p.meeting(cal, &ev, ev)
	p.people(&ev, "", "", nil)
	if len(ev.Attendees) > 0 && ev.Attendees[0].Email != p.me.Email {
		ev.Attendees = append([]models.Attendee{{Name: p.me.Name, Email: p.me.Email, Response: models.ResponseOrganizer}}, ev.Attendees...)
	}
	if ev.RRule != "" {
		ev.IsRecurring, ev.Recurrence = true, calendar.Summarize(ev.RRule)
		expand(ev, func(o calendar.Event) { m[o.RemoteID] = &o })
		return ev, nil
	}
	m[ev.RemoteID] = &ev
	return ev, nil
}

func (p *CalendarProvider) Update(ctx context.Context, cal calendar.Calendar, remoteID string, ev calendar.Event, scope string) (calendar.Event, error) {
	if err := p.sleep(ctx); err != nil {
		return calendar.Event{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.store(cal)
	if err != nil {
		return ev, err
	}
	cur, ok := m[remoteID]
	if !ok {
		return ev, errors.New("event not found")
	}
	apply := func(t *calendar.Event, shift time.Duration) {
		t.Title, t.Location, t.Description, t.TimeZone = ev.Title, ev.Location, ev.Description, ev.TimeZone
		t.Start = t.Start.Add(shift)
		t.End = t.Start.Add(ev.End.Sub(ev.Start))
		t.AllDay = ev.AllDay
		if ev.Attendees != nil {
			p.people(t, "", "", nil)
			t.Attendees = append(t.Attendees, ev.Attendees...)
		}
		if ev.Reminders != nil {
			t.Reminders = ev.Reminders
		}
		p.meeting(cal, t, ev)
		t.Updated = time.Now().UTC()
	}
	if scope == models.ScopeSeries && cur.SeriesRemoteID != "" {
		shift := ev.Start.Sub(cur.Start)
		for _, o := range m {
			if o.SeriesRemoteID == cur.SeriesRemoteID {
				apply(o, shift)
			}
		}
		return *m[remoteID], nil
	}
	apply(cur, ev.Start.Sub(cur.Start))
	return *cur, nil
}

// Move moves an event (or a whole series) between demo calendars.
func (p *CalendarProvider) Move(ctx context.Context, from, to calendar.Calendar, remoteID string, ev calendar.Event) (calendar.Event, error) {
	if err := p.sleep(ctx); err != nil {
		return calendar.Event{}, err
	}
	p.mu.Lock()
	src, err := p.store(from)
	dst, err2 := p.store(to)
	if err == nil {
		err = err2
	}
	if err != nil {
		p.mu.Unlock()
		return ev, err
	}
	var moved *calendar.Event
	for id, o := range src {
		if id == remoteID || o.SeriesRemoteID == remoteID {
			delete(src, id)
			dst[id] = o
			if id == remoteID || moved == nil {
				moved = o
			}
		}
	}
	p.mu.Unlock()
	if moved == nil {
		return ev, errors.New("event not found")
	}
	return p.Update(ctx, to, moved.RemoteID, ev, models.ScopeSeries)
}

func (p *CalendarProvider) Delete(ctx context.Context, cal calendar.Calendar, remoteID, scope, etag string) error {
	if err := p.sleep(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.store(cal)
	if err != nil {
		return err
	}
	cur, ok := m[remoteID]
	if !ok {
		// A series id: delete its occurrences.
		for id, o := range m {
			if o.SeriesRemoteID == remoteID {
				delete(m, id)
			}
		}
		return nil
	}
	if scope == models.ScopeSeries && cur.SeriesRemoteID != "" {
		for id, o := range m {
			if o.SeriesRemoteID == cur.SeriesRemoteID {
				delete(m, id)
			}
		}
		return nil
	}
	delete(m, remoteID)
	return nil
}

func (p *CalendarProvider) Respond(ctx context.Context, cal calendar.Calendar, remoteID, response string) error {
	if err := p.sleep(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.store(cal)
	if err != nil {
		return err
	}
	set := func(ev *calendar.Event) {
		ev.MyResponse = response
		for i := range ev.Attendees {
			if strings.EqualFold(ev.Attendees[i].Email, p.me.Email) {
				ev.Attendees[i].Response = response
			}
		}
	}
	if ev, ok := m[remoteID]; ok {
		set(ev)
		return nil
	}
	found := false
	for _, ev := range m {
		if ev.SeriesRemoteID == remoteID {
			set(ev)
			found = true
		}
	}
	if !found {
		return errors.New("event not found")
	}
	return nil
}

var _ calendar.Provider = (*CalendarProvider)(nil)
