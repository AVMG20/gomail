// Package calendar holds the provider-neutral calendar types exchanged
// between the calendar clients (provider/gcal, provider/graphcal, demo), the
// store and the core, plus shared helpers: Windows time zone names, RRULE
// building and human-readable recurrence summaries.
package calendar

import (
	"context"
	"errors"
	"time"
	_ "time/tzdata" // IANA zones on systems without zoneinfo (Windows)

	"github.com/AVMG20/gomail/internal/models"
)

// Calendar is a provider calendar.
type Calendar struct {
	RemoteID string
	Name     string
	Color    string // hex or ""
	Primary  bool
	CanEdit  bool
	TimeZone string // IANA, "" if unknown
	// DefaultReminders apply to events that use the calendar defaults.
	DefaultReminders []int
}

// Event is a provider event (or one occurrence of a series).
type Event struct {
	RemoteID       string
	SeriesRemoteID string // the series master's id for occurrences, else ""
	ICalUID        string
	Title          string
	Start, End     time.Time // UTC; all-day: dates at 00:00 UTC, End exclusive
	AllDay         bool
	TimeZone       string // IANA
	Location       string
	Description    string // plain text
	Attendees      []models.Attendee
	Organizer      models.Address
	IsOrganizer    bool
	MyResponse     string
	MeetingURL     string
	IsRecurring    bool
	Recurrence     string // human summary
	RRule          string
	Status         string
	Reminders      []int
	CanEdit        bool
	Updated        time.Time
	// OriginalStart is the occurrence's start before an edit; Update with
	// ScopeSeries shifts the series by Start - OriginalStart.
	OriginalStart time.Time
}

// SyncRequest asks for the events of a calendar in [Start, End).
type SyncRequest struct {
	Start, End time.Time
	// State is the opaque incremental state from the last round ("" = full).
	State string
}

// SyncResult is one round of calendar changes.
type SyncResult struct {
	Upserts []Event
	// Deleted lists remote ids that were cancelled or removed.
	Deleted []string
	// State replaces the stored state ("" = no incremental sync possible:
	// the next round is a full window fetch).
	State string
	// Full is true when Upserts is the complete content of the window:
	// local events of the window that aren't in it are removed.
	Full bool
}

// Provider is a calendar backend. Ids are remote ids.
type Provider interface {
	Calendars(ctx context.Context) ([]Calendar, error)
	// Sync returns the events of one calendar: incremental when the state
	// is still valid, otherwise a full fetch of the window.
	Sync(ctx context.Context, cal Calendar, req SyncRequest) (SyncResult, error)
	// Range fetches the events of [start, end) without touching sync state
	// (views outside the synced window).
	Range(ctx context.Context, cal Calendar, start, end time.Time) ([]Event, error)
	Create(ctx context.Context, cal Calendar, ev Event) (Event, error)
	// Update changes an event; with ScopeSeries the change applies to the
	// series of an occurrence (id may be an occurrence id).
	Update(ctx context.Context, cal Calendar, remoteID string, ev Event, scope string) (Event, error)
	Delete(ctx context.Context, cal Calendar, remoteID, scope string) error
	// Respond answers an invitation: accepted | tentative | declined.
	Respond(ctx context.Context, cal Calendar, remoteID, response string) error
}

// ErrNoConsent means the sign-in lacks calendar access (insufficient scope).
var ErrNoConsent = errors.New("calendar access was not granted for this account")

// ErrReadOnly is returned for changes to a read-only calendar.
var ErrReadOnly = errors.New("this calendar is read-only")

// WindowFor returns the default synced window around now: from the first
// day of the month three months back to twelve months ahead.
func WindowFor(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month()-3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(now.Year(), now.Month()+13, 1, 0, 0, 0, 0, time.UTC)
	return start, end
}

// DateUTC returns the calendar date of t (in its location) at 00:00 UTC.
func DateUTC(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// NormalizeInput fills defaults: end = start + 1h (all-day: + 1 day), dates
// for all-day events, a valid IANA zone.
func NormalizeInput(in *models.EventInput) error {
	if in.Start.IsZero() {
		return errors.New("the event needs a start time")
	}
	if in.AllDay {
		in.Start = DateUTC(in.Start)
		if !in.End.IsZero() {
			in.End = DateUTC(in.End)
		}
		if !in.End.After(in.Start) {
			in.End = in.Start.AddDate(0, 0, 1)
		}
	} else {
		in.Start = in.Start.UTC()
		in.End = in.End.UTC()
		if in.End.IsZero() {
			in.End = in.Start.Add(time.Hour)
		}
		if in.End.Before(in.Start) {
			return errors.New("the event ends before it starts")
		}
	}
	if in.TimeZone != "" {
		if _, err := time.LoadLocation(in.TimeZone); err != nil {
			in.TimeZone = ""
		}
	}
	switch in.Recurrence.Frequency {
	case "", models.RepeatNone:
		in.Recurrence = models.EventRecurrence{Frequency: models.RepeatNone}
	case models.RepeatDaily, models.RepeatWeekly, models.RepeatMonthly, models.RepeatYearly:
		if in.Recurrence.Count < 0 || in.Recurrence.Count > 1000 {
			return errors.New("the number of repetitions must be between 1 and 1000")
		}
	default:
		return errors.New("unknown repeat option " + in.Recurrence.Frequency)
	}
	if len(in.Title) > 1000 {
		in.Title = in.Title[:1000]
	}
	if len(in.Description) > 64<<10 {
		return errors.New("the description is too long (max 64 KB)")
	}
	if len(in.Attendees) > 500 {
		return errors.New("too many attendees (max 500)")
	}
	for _, m := range in.ReminderMinutes {
		if m < 0 || m > 40320 {
			return errors.New("reminders must be between 0 minutes and 4 weeks before the event")
		}
	}
	return nil
}

// FromInput converts an EventInput to a provider event.
func FromInput(in models.EventInput) Event {
	ev := Event{Title: in.Title, Start: in.Start, End: in.End, AllDay: in.AllDay, TimeZone: in.TimeZone,
		Location: in.Location, Description: in.Description, Attendees: in.Attendees, Reminders: in.ReminderMinutes,
		Status: models.EventConfirmed}
	if r := BuildRRule(in.Recurrence, in.Start, in.TimeZone); r != "" {
		ev.RRule, ev.IsRecurring, ev.Recurrence = r, true, Summarize(r)
	}
	return ev
}
