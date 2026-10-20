package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider/gcal"
	"github.com/AVMG20/gomail/internal/provider/graphcal"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncer"
)

// calState holds calendar clients and per-account sync locks.
type calState struct {
	mu        sync.Mutex
	providers map[string]calendar.Provider
	locks     map[string]*sync.Mutex
	fetched   map[string]time.Time // calendar id + range -> when an out-of-window range was fetched
}

func (c *calState) init() {
	c.providers, c.locks, c.fetched = map[string]calendar.Provider{}, map[string]*sync.Mutex{}, map[string]time.Time{}
}

func (c *calState) lock(accountID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.locks[accountID]
	if !ok {
		l = &sync.Mutex{}
		c.locks[accountID] = l
	}
	return l
}

// Calendar request limits.
const (
	maxEventRange    = 400 * 24 * time.Hour
	rangeRefetchWait = 10 * time.Minute
)

// nowFunc is the clock for sync windows (tests).
var nowFunc = time.Now

func (s *Service) recordCalendarConsent(accountID, kind string, tok *oauth2.Token) {
	granted, _ := auth.CalendarGranted(kind, tok)
	_ = s.store.SetCalendarConsent(accountID, granted)
}

func (s *Service) isDemo(accountID string) bool {
	return s.opts.Demo && strings.HasPrefix(accountID, "demo-")
}

func (s *Service) calendarProvider(acct models.Account) (calendar.Provider, error) {
	s.cal.mu.Lock()
	p, ok := s.cal.providers[acct.ID]
	s.cal.mu.Unlock()
	if ok {
		return p, nil
	}
	var err error
	switch {
	case s.opts.CalendarFactory != nil:
		p, err = s.opts.CalendarFactory(acct)
	case s.isDemo(acct.ID):
		p = demo.NewCalendar(acct)
	default:
		var ts *auth.TokenSource
		if ts, err = s.tokenSource(acct); err == nil {
			switch acct.Provider {
			case models.ProviderGmail:
				p = gcal.New(ts, s.opts.GoogleCalendarBase)
			case models.ProviderOutlook:
				p = graphcal.New(ts, s.opts.GraphBase)
			default:
				err = fmt.Errorf("unsupported provider %q", acct.Provider)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	s.cal.mu.Lock()
	if existing, ok := s.cal.providers[acct.ID]; ok {
		p = existing
	} else {
		s.cal.providers[acct.ID] = p
	}
	s.cal.mu.Unlock()
	return p, nil
}

func (s *Service) emitCalendar(accountID string) {
	s.emit(models.EventCalendarChanged, models.CalendarChanged{AccountID: accountID})
}

// calendarAccount loads an account whose calendars may be used.
func (s *Service) calendarAccount(accountID string) (models.Account, error) {
	acct, err := s.store.Account(accountID)
	if err != nil {
		return acct, errors.New("account not found")
	}
	if !acct.CalendarConsent {
		return acct, fmt.Errorf("calendar access isn't enabled for %s yet. Choose \"Enable calendar\" to grant it", acct.Email)
	}
	if !acct.CalendarEnabled {
		return acct, fmt.Errorf("the calendar of %s is disabled", acct.Email)
	}
	return acct, nil
}

// calendarError makes provider errors readable and withdraws consent when
// the provider says the sign-in lacks calendar scopes.
func (s *Service) calendarError(acct models.Account, err error) error {
	if errors.Is(err, calendar.ErrNoConsent) {
		_ = s.store.SetCalendarConsent(acct.ID, false)
		s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: acct.ID})
		s.emitCalendar(acct.ID)
		return fmt.Errorf("gomail doesn't have access to the calendar of %s. Choose \"Enable calendar\" to grant it", acct.Email)
	}
	if errors.Is(err, calendar.ErrReadOnly) {
		return errors.New("this calendar is read-only")
	}
	if errors.Is(err, calendar.ErrConflict) {
		return errors.New("this event changed elsewhere. gomail loaded the latest version; please make your change again")
	}
	return errors.New(syncer.HumanError(acct, err))
}

// ---- sync ----

func (s *Service) syncCalendarsQuiet(ctx context.Context, accountID string) {
	if err := s.syncCalendars(ctx, accountID); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		slog.Warn("calendar sync failed", "account", accountID, "err", err)
	}
}

// syncCalendars syncs every calendar of an account (hidden ones too: the
// checkboxes are only view filters). It is a no-op without consent or with
// the account's calendar disabled.
func (s *Service) syncCalendars(ctx context.Context, accountID string) error {
	acct, err := s.store.Account(accountID)
	if err != nil || !acct.CalendarConsent || !acct.CalendarEnabled {
		return nil
	}
	l := s.cal.lock(accountID)
	l.Lock()
	defer l.Unlock()
	p, err := s.calendarProvider(acct)
	if err != nil {
		return err
	}
	cals, err := p.Calendars(ctx)
	if err != nil {
		return s.calendarError(acct, err)
	}
	if err := s.store.ReplaceCalendars(acct.ID, cals); err != nil {
		return err
	}
	rows, err := s.store.Calendars(acct.ID)
	if err != nil {
		return err
	}
	changed := false
	var firstErr error
	for _, row := range rows {
		ch, err := s.syncCalendar(ctx, acct, p, row)
		if err != nil {
			if errors.Is(err, calendar.ErrNoConsent) {
				return s.calendarError(acct, err)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", row.Name, syncer.HumanError(acct, err))
			}
			continue
		}
		changed = changed || ch
	}
	if changed {
		s.emitCalendar(acct.ID)
	}
	return firstErr
}

// syncCalendar runs one round for a calendar. A moved default window (a
// new month) restarts with a full fetch.
func (s *Service) syncCalendar(ctx context.Context, acct models.Account, p calendar.Provider, row store.CalendarRow) (bool, error) {
	start, end := calendar.WindowFor(nowFunc())
	state := row.State
	if !row.WindowStart.Equal(start) || !row.WindowEnd.Equal(end) {
		state = ""
	}
	res, err := p.Sync(ctx, row.Provider(), calendar.SyncRequest{Start: start, End: end, State: state})
	if err != nil {
		return false, err
	}
	if _, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: res.Upserts, Deleted: res.Deleted,
		Replace: res.Full, Start: start, End: end}); err != nil {
		return false, err
	}
	if err := s.store.SetCalendarSync(row.ID, res.State, start, end); err != nil {
		return false, err
	}
	return len(res.Upserts) > 0 || len(res.Deleted) > 0 || res.Full, nil
}

// SyncCalendars syncs the calendars of one account ("" = all) now.
func (s *Service) SyncCalendars(accountID string) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	if accountID != "" {
		if _, err := s.calendarAccount(accountID); err != nil {
			return err
		}
		return s.syncCalendars(ctx, accountID)
	}
	accts, err := s.store.Accounts()
	if err != nil {
		return err
	}
	var errs []string
	for _, a := range accts {
		if err := s.syncCalendars(ctx, a.ID); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---- calendars ----

// ListCalendars lists the calendars of accounts with calendar access
// (including accounts whose calendar is turned off, for their toggles).
func (s *Service) ListCalendars() ([]models.Calendar, error) {
	accts, err := s.store.Accounts()
	if err != nil {
		return nil, err
	}
	ok := map[string]bool{}
	for _, a := range accts {
		ok[a.ID] = a.CalendarConsent
	}
	rows, err := s.store.Calendars("")
	if err != nil {
		return nil, err
	}
	out := []models.Calendar{}
	for _, r := range rows {
		if ok[r.AccountID] {
			out = append(out, r.Calendar)
		}
	}
	return out, nil
}

// SetCalendarVisible shows or hides one calendar in the unified view. It is
// a view filter only: hidden calendars keep syncing and reminding.
func (s *Service) SetCalendarVisible(calendarID string, visible bool) error {
	row, err := s.store.Calendar(calendarID)
	if err != nil {
		return errors.New("calendar not found")
	}
	if err := s.store.SetCalendarVisible(calendarID, visible); err != nil {
		return err
	}
	s.emitCalendar(row.AccountID)
	return nil
}

// SetAccountCalendarEnabled disables or re-enables an account's calendar.
// Disabled: no calendar sync, no reminders and its events aren't listed
// (its calendars stay in ListCalendars). Re-enabling syncs it right away.
func (s *Service) SetAccountCalendarEnabled(accountID string, enabled bool) error {
	if err := s.store.SetCalendarEnabled(accountID, enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("account not found")
		}
		return err
	}
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	s.emitCalendar(accountID)
	if enabled {
		s.goBG(func() { s.syncCalendarsQuiet(s.ctx, accountID) })
	}
	return nil
}

// SetAccountCalendarMuted silences an account's event reminders; its
// events keep syncing and showing.
func (s *Service) SetAccountCalendarMuted(accountID string, muted bool) error {
	if err := s.store.SetCalendarMuted(accountID, muted); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("account not found")
		}
		return err
	}
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	return nil
}

// EnableCalendar signs in again with the calendar scopes (incremental
// authorization; mail data is kept) and starts syncing the calendars.
func (s *Service) EnableCalendar(accountID string) (models.Account, error) {
	acct, err := s.store.Account(accountID)
	if err != nil {
		return acct, errors.New("account not found")
	}
	if !s.isDemo(acct.ID) {
		prof, tok, err := s.signIn(acct.Provider)
		if err != nil {
			return acct, err
		}
		if !strings.EqualFold(prof.Email, acct.Email) {
			return acct, fmt.Errorf("you signed in as %s, but this account is %s. Sign in with %s to enable its calendar",
				prof.Email, acct.Email, acct.Email)
		}
		if granted, _ := auth.CalendarGranted(acct.Provider, tok); !granted {
			return acct, errors.New("calendar access wasn't granted. Try again and allow gomail to see and edit your calendars")
		}
		if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
			return acct, fmt.Errorf("could not store the sign-in securely: %w", err)
		}
		s.forgetClients(acct.ID)
		_ = s.store.SetNeedsReauth(acct.ID, false)
	}
	if err := s.store.SetCalendarConsent(acct.ID, true); err != nil {
		return acct, err
	}
	_ = s.store.SetCalendarEnabled(acct.ID, true)
	slog.Info("calendar enabled", "account", acct.ID)
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: acct.ID})
	s.goBG(func() { s.syncCalendarsQuiet(s.ctx, acct.ID) })
	return s.store.Account(acct.ID)
}

// ---- events ----

// ListEvents returns the events overlapping [start, end) of visible
// calendars of enabled accounts, from the local cache. Ranges outside the
// synced window are fetched in the background (calendar:changed follows).
func (s *Service) ListEvents(start, end time.Time) ([]models.CalendarEvent, error) {
	if !end.After(start) {
		return nil, errors.New("the end of the range must be after its start")
	}
	if end.Sub(start) > maxEventRange {
		return nil, errors.New("the range is too long (max 400 days)")
	}
	evs, err := s.store.Events(start, end, s.Settings().CalendarShowDeclined)
	if err != nil {
		return nil, err
	}
	s.fetchOutsideWindow(start, end)
	return evs, nil
}

// fetchOutsideWindow fetches and caches the part of [start, end) that lies
// outside the synced window, once per calendar and range every 10 minutes.
func (s *Service) fetchOutsideWindow(start, end time.Time) {
	wStart, wEnd := calendar.WindowFor(nowFunc())
	if !start.Before(wStart) && !end.After(wEnd) {
		return
	}
	accts, err := s.store.Accounts()
	if err != nil {
		return
	}
	for _, a := range accts {
		if !a.CalendarConsent || !a.CalendarEnabled {
			continue
		}
		rows, err := s.store.Calendars(a.ID)
		if err != nil {
			continue
		}
		for _, row := range rows {
			if !row.Visible {
				continue
			}
			var ranges [][2]time.Time
			if start.Before(wStart) {
				ranges = append(ranges, [2]time.Time{start, minTime(end, wStart)})
			}
			if end.After(wEnd) {
				ranges = append(ranges, [2]time.Time{maxTime(start, wEnd), end})
			}
			for _, r := range ranges {
				key := fmt.Sprintf("%s|%d|%d", row.ID, r[0].Unix(), r[1].Unix())
				s.cal.mu.Lock()
				last, seen := s.cal.fetched[key]
				if seen && time.Since(last) < rangeRefetchWait {
					s.cal.mu.Unlock()
					continue
				}
				s.cal.fetched[key] = time.Now()
				s.cal.mu.Unlock()
				acct, row, r := a, row, r
				s.goBG(func() {
					p, err := s.calendarProvider(acct)
					if err != nil {
						return
					}
					ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
					defer cancel()
					evs, err := p.Range(ctx, row.Provider(), r[0], r[1])
					if err != nil {
						slog.Warn("fetching calendar range failed", "calendar", row.ID, "err", err)
						s.cal.mu.Lock()
						delete(s.cal.fetched, key)
						s.cal.mu.Unlock()
						return
					}
					if _, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: evs, Replace: true, Start: r[0], End: r[1]}); err == nil {
						s.emitCalendar(acct.ID)
					}
				})
			}
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// GetEvent returns one event.
func (s *Service) GetEvent(id string) (models.CalendarEvent, error) {
	row, err := s.store.Event(id)
	if err != nil {
		return models.CalendarEvent{}, errors.New("that event no longer exists")
	}
	return row.CalendarEvent, nil
}

// targetCalendar picks the calendar for a new event: the given one or the
// account's primary (else first writable) calendar.
func (s *Service) targetCalendar(acct models.Account, calendarID string) (store.CalendarRow, error) {
	if calendarID != "" {
		row, err := s.store.Calendar(calendarID)
		if err != nil || row.AccountID != acct.ID {
			return row, errors.New("calendar not found")
		}
		return row, nil
	}
	rows, err := s.store.Calendars(acct.ID)
	if err != nil {
		return store.CalendarRow{}, err
	}
	for _, r := range rows {
		if r.Primary && r.CanEdit {
			return r, nil
		}
	}
	for _, r := range rows {
		if r.CanEdit {
			return r, nil
		}
	}
	return store.CalendarRow{}, fmt.Errorf("%s has no writable calendar (sync the calendar first)", acct.Email)
}

func (s *Service) calCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, 2*time.Minute)
}

// CreateEvent creates an event. Single events appear right away and are
// removed again if the provider refuses; series are synced in after
// creation (their occurrences come from the provider).
func (s *Service) CreateEvent(in models.EventInput) (models.CalendarEvent, error) {
	if err := calendar.NormalizeInput(&in); err != nil {
		return models.CalendarEvent{}, err
	}
	acct, err := s.calendarAccount(in.AccountID)
	if err != nil {
		return models.CalendarEvent{}, err
	}
	row, err := s.targetCalendar(acct, in.CalendarID)
	if err != nil {
		return models.CalendarEvent{}, err
	}
	if !row.CanEdit {
		return models.CalendarEvent{}, errors.New("this calendar is read-only")
	}
	if in.AddVideoCall && row.VideoCallProvider == "" {
		return models.CalendarEvent{}, errors.New("this calendar can't add video calls; paste a meeting link instead")
	}
	p, err := s.calendarProvider(acct)
	if err != nil {
		return models.CalendarEvent{}, err
	}
	ev := calendar.FromInput(in)
	ev.IsOrganizer, ev.MyResponse, ev.CanEdit = true, models.ResponseOrganizer, true
	ev.Organizer = models.Address{Name: acct.DisplayName, Email: acct.Email}
	if ev.Reminders == nil {
		ev.Reminders = row.DefaultReminders
	}
	// Optimistic placeholder for single events.
	placeholder := ""
	if ev.RRule == "" {
		tmp := ev
		tmp.RemoteID = randomID("local-")
		ids, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: []calendar.Event{tmp}})
		if err == nil && len(ids) == 1 {
			placeholder = ids[0]
			s.emitCalendar(acct.ID)
		}
	}
	ctx, cancel := s.calCtx()
	defer cancel()
	created, err := p.Create(ctx, row.Provider(), ev)
	if placeholder != "" {
		_ = s.store.DeleteEvents([]string{placeholder})
	}
	if err != nil {
		s.emitCalendar(acct.ID)
		return models.CalendarEvent{}, fmt.Errorf("creating the event failed: %w", s.calendarError(acct, err))
	}
	if created.RRule == "" && !created.IsRecurring {
		ids, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: []calendar.Event{created}})
		s.emitCalendar(acct.ID)
		if err != nil || len(ids) == 0 {
			return models.CalendarEvent{}, err
		}
		return s.GetEvent(ids[0])
	}
	// A series: fetch its occurrences and return the first one.
	if _, err := s.syncCalendar(ctx, acct, p, row); err != nil {
		slog.Warn("syncing a new series failed", "err", err)
	}
	s.emitCalendar(acct.ID)
	occ, err := s.store.SeriesEvents(row.ID, created.RemoteID)
	if err == nil && len(occ) > 0 {
		first := occ[0]
		for _, o := range occ {
			if o.Start.Before(first.Start) {
				first = o
			}
		}
		return first.CalendarEvent, nil
	}
	out := created
	return models.CalendarEvent{CalendarID: row.ID, AccountID: acct.ID, Title: out.Title, Start: out.Start, End: out.End,
		AllDay: out.AllDay, TimeZone: out.TimeZone, Location: out.Location, Description: out.Description, Attendees: out.Attendees,
		Organizer: out.Organizer, IsOrganizer: true, MyResponse: models.ResponseOrganizer, IsRecurring: true, RRule: out.RRule,
		Recurrence: out.Recurrence, Status: out.Status, Reminders: out.Reminders, CanEdit: true}, nil
}

// eventContext loads an event with its calendar, account and provider.
func (s *Service) eventContext(id string) (store.EventRow, store.CalendarRow, models.Account, calendar.Provider, error) {
	ev, err := s.store.Event(id)
	if err != nil {
		return ev, store.CalendarRow{}, models.Account{}, nil, errors.New("that event no longer exists")
	}
	row, err := s.store.Calendar(ev.CalendarID)
	if err != nil {
		return ev, row, models.Account{}, nil, errors.New("that calendar no longer exists")
	}
	acct, err := s.calendarAccount(ev.AccountID)
	if err != nil {
		return ev, row, acct, nil, err
	}
	p, err := s.calendarProvider(acct)
	return ev, row, acct, p, err
}

func normScope(scope string) string {
	if scope == models.ScopeSeries {
		return models.ScopeSeries
	}
	return models.ScopeThis
}

// UpdateEvent changes an event; scope "series" applies the change to every
// occurrence (times shift by the change made to this one).
func (s *Service) UpdateEvent(id string, in models.EventInput, scope string) (models.CalendarEvent, error) {
	cur, row, acct, p, err := s.eventContext(id)
	if err != nil {
		return models.CalendarEvent{}, err
	}
	if !cur.CanEdit {
		return models.CalendarEvent{}, errors.New("you can't change this event: only its organizer can")
	}
	if err := calendar.NormalizeInput(&in); err != nil {
		return models.CalendarEvent{}, err
	}
	scope = normScope(scope)
	if in.CalendarID != "" && in.CalendarID != cur.CalendarID {
		return s.moveEvent(cur, row, acct, p, in, scope)
	}
	if in.AddVideoCall && row.VideoCallProvider == "" && cur.MeetingURL == "" {
		return models.CalendarEvent{}, errors.New("this calendar can't add video calls; paste a meeting link instead")
	}
	ev := calendar.FromInput(in)
	ev.AddVideoCall = in.AddVideoCall && cur.MeetingURL == "" // keep an existing call
	ev.SeriesRemoteID, ev.OriginalStart, ev.ICalUID = cur.SeriesRemoteID, cur.Start, cur.ICalUID
	if scope == models.ScopeThis {
		ev.ETag = cur.ETag
	}
	if cur.SeriesRemoteID == "" {
		scope = models.ScopeThis
	} else if scope == models.ScopeThis {
		ev.RRule = "" // an occurrence can't get its own rule
	}
	// Optimistic local change.
	old := []store.EventRow{cur}
	if scope == models.ScopeSeries {
		if old, err = s.store.SeriesEvents(cur.CalendarID, cur.SeriesRemoteID); err != nil {
			return models.CalendarEvent{}, err
		}
	}
	shift := ev.Start.Sub(cur.Start)
	var next []calendar.Event
	for _, o := range old {
		n := o.ToProvider()
		n.Title, n.Location, n.Description, n.AllDay, n.TimeZone = ev.Title, ev.Location, ev.Description, ev.AllDay, ev.TimeZone
		if ev.MeetingURL != "" {
			n.MeetingURL = ev.MeetingURL
		}
		if in.Attendees != nil {
			n.Attendees = in.Attendees
		}
		if in.ReminderMinutes != nil {
			n.Reminders = in.ReminderMinutes
		}
		if o.ID == cur.ID {
			n.Start, n.End = ev.Start, ev.End
		} else {
			n.Start = o.Start.Add(shift)
			n.End = n.Start.Add(ev.End.Sub(ev.Start))
		}
		next = append(next, n)
	}
	if _, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: next}); err != nil {
		return models.CalendarEvent{}, err
	}
	s.emitCalendar(acct.ID)
	ctx, cancel := s.calCtx()
	defer cancel()
	if in.Attendees == nil {
		ev.Attendees = nil // keep the provider's guest list
	}
	updated, err := p.Update(ctx, row.Provider(), cur.RemoteID, ev, scope)
	if err != nil {
		_ = s.store.PutEventRows(old)
		s.emitCalendar(acct.ID)
		return models.CalendarEvent{}, s.eventChangeError(acct, p, row, "saving the event failed", err)
	}
	if scope == models.ScopeThis && updated.RemoteID == cur.RemoteID && ev.RRule == "" {
		_, _ = s.store.ApplyEvents(row, store.EventChanges{Upserts: []calendar.Event{updated}})
	} else {
		if ev.RRule != "" && cur.SeriesRemoteID == "" {
			// Became a series: its occurrences replace the single event.
			_ = s.store.DeleteEvents([]string{cur.ID})
		}
		// Series changes: take the provider's view of the occurrences.
		s.goBG(func() {
			ctx, cancel := s.calCtx()
			defer cancel()
			if _, err := s.syncCalendar(ctx, acct, p, row); err == nil {
				s.emitCalendar(acct.ID)
			}
		})
	}
	s.emitCalendar(acct.ID)
	return s.GetEvent(id)
}

// DeleteEvent deletes an event (scope "series": the whole series).
func (s *Service) DeleteEvent(id, scope string) error {
	cur, row, acct, p, err := s.eventContext(id)
	if err != nil {
		return err
	}
	if !row.CanEdit {
		return errors.New("this calendar is read-only")
	}
	scope = normScope(scope)
	old := []store.EventRow{cur}
	target := cur.RemoteID
	if scope == models.ScopeSeries && cur.SeriesRemoteID != "" {
		if old, err = s.store.SeriesEvents(cur.CalendarID, cur.SeriesRemoteID); err != nil {
			return err
		}
		target = cur.SeriesRemoteID
	}
	var ids []string
	for _, o := range old {
		ids = append(ids, o.ID)
	}
	if err := s.store.DeleteEvents(ids); err != nil {
		return err
	}
	s.emitCalendar(acct.ID)
	ctx, cancel := s.calCtx()
	defer cancel()
	etag := ""
	if target == cur.RemoteID {
		etag = cur.ETag
	}
	if err := p.Delete(ctx, row.Provider(), target, scope, etag); err != nil {
		_ = s.store.PutEventRows(old)
		s.emitCalendar(acct.ID)
		return s.eventChangeError(acct, p, row, "deleting the event failed", err)
	}
	return nil
}

// RespondToEvent answers an invitation (accepted | tentative | declined).
// Occurrences of a series answer for the whole series.
func (s *Service) RespondToEvent(id, response string) error {
	switch response {
	case models.ResponseAccepted, models.ResponseTentative, models.ResponseDeclined:
	default:
		return errors.New("the response must be accepted, tentative or declined")
	}
	cur, row, acct, p, err := s.eventContext(id)
	if err != nil {
		return err
	}
	if cur.IsOrganizer {
		return errors.New("you're the organizer of this event")
	}
	old := []store.EventRow{cur}
	target := cur.RemoteID
	if cur.SeriesRemoteID != "" {
		if old, err = s.store.SeriesEvents(cur.CalendarID, cur.SeriesRemoteID); err != nil {
			return err
		}
		target = cur.SeriesRemoteID
	}
	var next []calendar.Event
	for _, o := range old {
		n := o.ToProvider()
		n.MyResponse = response
		for i := range n.Attendees {
			if strings.EqualFold(n.Attendees[i].Email, acct.Email) {
				n.Attendees[i].Response = response
			}
		}
		next = append(next, n)
	}
	if _, err := s.store.ApplyEvents(row, store.EventChanges{Upserts: next}); err != nil {
		return err
	}
	s.emitCalendar(acct.ID)
	ctx, cancel := s.calCtx()
	defer cancel()
	if err := p.Respond(ctx, row.Provider(), target, response); err != nil {
		_ = s.store.PutEventRows(old)
		s.emitCalendar(acct.ID)
		return fmt.Errorf("sending your response failed: %w", s.calendarError(acct, err))
	}
	return nil
}

// inviteEvent links a message invitation to a synced event (by iCalUID).
func (s *Service) inviteEvent(m *models.Message) {
	if m.Invite == nil || m.Invite.UID == "" {
		return
	}
	acct, err := s.store.Account(m.AccountID)
	if err != nil || !acct.CalendarConsent || !acct.CalendarEnabled {
		return
	}
	evs, err := s.store.EventsByICalUID(m.AccountID, m.Invite.UID)
	if err != nil || len(evs) == 0 {
		return
	}
	best := evs[0]
	for _, e := range evs {
		if !e.Start.Before(m.Invite.Start) {
			best = e
			break
		}
	}
	m.Invite.EventID = best.ID
}

// eventChangeError reports a failed change. On a conflict (the event was
// changed elsewhere) the calendar is resynced first, so the UI shows the
// latest version when it reloads.
func (s *Service) eventChangeError(acct models.Account, p calendar.Provider, row store.CalendarRow, what string, err error) error {
	if errors.Is(err, calendar.ErrConflict) {
		ctx, cancel := s.calCtx()
		defer cancel()
		if _, serr := s.syncCalendar(ctx, acct, p, row); serr == nil {
			s.emitCalendar(acct.ID)
		}
		return s.calendarError(acct, err)
	}
	return fmt.Errorf("%s: %w", what, s.calendarError(acct, err))
}

// MoveEventCheck tells what moving an event to calendarID would do.
func (s *Service) MoveEventCheck(id, calendarID string) (models.MoveCheck, error) {
	cur, err := s.store.Event(id)
	if err != nil {
		return models.MoveCheck{}, errors.New("that event no longer exists")
	}
	return s.moveCheck(cur, calendarID)
}

func (s *Service) moveCheck(cur store.EventRow, calendarID string) (models.MoveCheck, error) {
	target, err := s.store.Calendar(calendarID)
	if err != nil {
		return models.MoveCheck{}, errors.New("calendar not found")
	}
	src, err := s.store.Calendar(cur.CalendarID)
	if err != nil {
		return models.MoveCheck{}, errors.New("that calendar no longer exists")
	}
	acct, _ := s.store.Account(cur.AccountID)
	others := 0
	for _, a := range cur.Attendees {
		if !strings.EqualFold(a.Email, acct.Email) && a.Response != models.ResponseOrganizer {
			others++
		}
	}
	mc := models.MoveCheck{Allowed: true, CrossAccount: target.AccountID != cur.AccountID, HasAttendees: others > 0,
		Recurring: cur.SeriesRemoteID != ""}
	switch {
	case target.ID == cur.CalendarID:
		mc.Allowed, mc.Reason = false, "the event is already in this calendar"
	case !target.CanEdit:
		mc.Allowed, mc.Reason = false, "the target calendar is read-only"
	case !src.CanEdit || !cur.IsOrganizer:
		mc.Allowed, mc.Reason = false, "only the organizer can move this event"
	case mc.Recurring && mc.CrossAccount && cur.RRule == "":
		mc.Allowed, mc.Reason = false, "this series can't be recreated in another account"
	}
	if mc.Allowed {
		if a, err := s.calendarAccount(target.AccountID); err != nil {
			mc.Allowed, mc.Reason = false, err.Error()
		} else if a.Provider == models.ProviderOutlook && !mc.CrossAccount {
			// Graph has no move: the event is recreated.
			mc.HasAttendees = others > 0
		} else if !mc.CrossAccount {
			mc.HasAttendees = false // Google moves keep the invitation
		}
	}
	return mc, nil
}

// moveEvent moves an event (a whole series for occurrences) to another
// calendar: natively within a Google account, otherwise by creating it in
// the target and deleting the original.
func (s *Service) moveEvent(cur store.EventRow, row store.CalendarRow, acct models.Account, p calendar.Provider,
	in models.EventInput, scope string) (models.CalendarEvent, error) {
	mc, err := s.moveCheck(cur, in.CalendarID)
	if err != nil {
		return models.CalendarEvent{}, err
	}
	if !mc.Allowed {
		return models.CalendarEvent{}, errors.New("can't move this event: " + mc.Reason)
	}
	if mc.Recurring && scope != models.ScopeSeries {
		return models.CalendarEvent{}, errors.New("only a whole series can be moved to another calendar")
	}
	target, _ := s.store.Calendar(in.CalendarID)
	tacct, _ := s.store.Account(target.AccountID)
	ev := calendar.FromInput(in)
	remote := cur.RemoteID
	if mc.Recurring {
		remote = cur.SeriesRemoteID
		if ev.RRule == "" {
			ev.RRule = cur.RRule
		}
	}
	if ev.Attendees == nil {
		ev.Attendees = cur.Attendees
	}
	old := []store.EventRow{cur}
	if mc.Recurring {
		if old, err = s.store.SeriesEvents(cur.CalendarID, cur.SeriesRemoteID); err != nil {
			return models.CalendarEvent{}, err
		}
	}
	ctx, cancel := s.calCtx()
	defer cancel()
	var moved calendar.Event
	if mc.CrossAccount {
		tp, err := s.calendarProvider(tacct)
		if err != nil {
			return models.CalendarEvent{}, err
		}
		if moved, err = tp.Create(ctx, target.Provider(), ev); err != nil {
			return models.CalendarEvent{}, fmt.Errorf("moving the event failed: %w", s.calendarError(tacct, err))
		}
		if err := p.Delete(ctx, row.Provider(), remote, models.ScopeSeries, ""); err != nil {
			slog.Warn("removing the moved event's original failed", "err", err)
			s.goBG(func() { s.syncCalendarsQuiet(s.ctx, tacct.ID) })
			return models.CalendarEvent{}, fmt.Errorf("the event was copied to %s, but the original couldn't be removed: %s", tacct.Email,
				s.calendarError(acct, err))
		}
	} else if moved, err = p.Move(ctx, row.Provider(), target.Provider(), remote, ev); err != nil {
		return models.CalendarEvent{}, fmt.Errorf("moving the event failed: %w", s.calendarError(acct, err))
	}
	var ids []string
	for _, o := range old {
		ids = append(ids, o.ID)
	}
	_ = s.store.DeleteEvents(ids)
	s.emitCalendar(acct.ID)
	tp, err := s.calendarProvider(tacct)
	if err == nil {
		_, _ = s.syncCalendar(ctx, tacct, tp, target)
	}
	if !moved.IsRecurring && moved.SeriesRemoteID == "" {
		if got, err := s.store.ApplyEvents(target, store.EventChanges{Upserts: []calendar.Event{moved}}); err == nil && len(got) == 1 {
			s.emitCalendar(tacct.ID)
			return s.GetEvent(got[0])
		}
	}
	s.emitCalendar(tacct.ID)
	if occ, err := s.store.SeriesEvents(target.ID, moved.RemoteID); err == nil && len(occ) > 0 {
		first := occ[0]
		for _, o := range occ {
			if o.Start.Before(first.Start) {
				first = o
			}
		}
		return first.CalendarEvent, nil
	}
	if got, err := s.store.Event(store.EventID(target.ID, moved.RemoteID)); err == nil {
		return got.CalendarEvent, nil
	}
	return models.CalendarEvent{}, errors.New("the event was moved, but it isn't synced yet")
}
