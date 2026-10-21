package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/models"
)

// CalendarID returns the deterministic local id of a remote calendar.
func CalendarID(accountID, remoteID string) string { return hashID("c", accountID, remoteID) }

// EventID returns the deterministic local id of a remote event.
func EventID(calendarID, remoteID string) string { return hashID("e", calendarID, remoteID) }

// CalendarRow is a stored calendar with its sync bookkeeping.
type CalendarRow struct {
	models.Calendar
	TimeZone         string
	DefaultReminders []int
	State            string
	WindowStart      time.Time
	WindowEnd        time.Time
}

// Provider returns the provider view of the calendar.
func (c CalendarRow) Provider() calendar.Calendar {
	return calendar.Calendar{RemoteID: c.RemoteID, Name: c.Name, Color: c.Color, Primary: c.Primary, CanEdit: c.CanEdit,
		TimeZone: c.TimeZone, DefaultReminders: c.DefaultReminders, VideoCallProvider: c.VideoCallProvider}
}

// color is the user's pick when there is one (SetCalendarColor), else the provider's.
const calendarCols = `id, account_id, remote_id, name, CASE WHEN color_override != '' THEN color_override ELSE color END, is_primary, can_edit, visible, time_zone, reminders, sync_state, window_start, window_end, video_provider`

func scanCalendar(sc interface{ Scan(...any) error }) (CalendarRow, error) {
	var c CalendarRow
	var primary, canEdit, visible int
	var rem string
	var ws, we int64
	err := sc.Scan(&c.ID, &c.AccountID, &c.RemoteID, &c.Name, &c.Color, &primary, &canEdit, &visible, &c.TimeZone, &rem, &c.State, &ws, &we,
		&c.VideoCallProvider)
	c.Primary, c.CanEdit, c.Visible = primary == 1, canEdit == 1, visible == 1
	if rem != "" {
		_ = json.Unmarshal([]byte(rem), &c.DefaultReminders)
	}
	c.WindowStart, c.WindowEnd = fromMillis(ws), fromMillis(we)
	return c, err
}

// ReplaceCalendars upserts an account's calendars (keeping the visible
// flag and sync state) and deletes the ones that disappeared.
func (s *Store) ReplaceCalendars(accountID string, cals []calendar.Calendar) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		keep := map[string]bool{}
		for _, c := range cals {
			id := CalendarID(accountID, c.RemoteID)
			keep[id] = true
			rem, _ := json.Marshal(c.DefaultReminders)
			if _, err := tx.Exec(`INSERT INTO calendars(id, account_id, remote_id, name, color, is_primary, can_edit, time_zone, reminders, video_provider)
				VALUES(?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, color = excluded.color, is_primary = excluded.is_primary,
					can_edit = excluded.can_edit, time_zone = excluded.time_zone, reminders = excluded.reminders,
					video_provider = excluded.video_provider`,
				id, accountID, c.RemoteID, c.Name, c.Color, boolInt(c.Primary), boolInt(c.CanEdit), c.TimeZone, string(rem),
				c.VideoCallProvider); err != nil {
				return err
			}
		}
		rows, err := tx.Query(`SELECT id FROM calendars WHERE account_id = ?`, accountID)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && !keep[id] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			if _, err := tx.Exec(`DELETE FROM calendars WHERE id = ?`, id); err != nil {
				return err
			}
		}
		// Preferences restored from a backup, waiting for these calendars.
		return applyPendingCalendars(tx, accountID)
	})
}

// Calendars lists calendars ("" = all accounts), primary first.
func (s *Store) Calendars(accountID string) ([]CalendarRow, error) {
	q := `SELECT ` + calendarCols + ` FROM calendars`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY account_id, is_primary DESC, name COLLATE NOCASE`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarRow
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Calendar returns one calendar.
func (s *Store) Calendar(id string) (CalendarRow, error) {
	c, err := scanCalendar(s.db.QueryRow(`SELECT `+calendarCols+` FROM calendars WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SetCalendarVisible shows or hides a calendar in the unified view.
func (s *Store) SetCalendarVisible(id string, visible bool) error {
	res, err := s.db.Exec(`UPDATE calendars SET visible = ? WHERE id = ?`, boolInt(visible), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCalendarColor sets the color shown for a calendar ("" = the provider's).
func (s *Store) SetCalendarColor(id, color string) error {
	res, err := s.db.Exec(`UPDATE calendars SET color_override = ? WHERE id = ?`, color, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCalendarSync stores the sync state and the synced window.
func (s *Store) SetCalendarSync(id, state string, start, end time.Time) error {
	_, err := s.db.Exec(`UPDATE calendars SET sync_state = ?, window_start = ?, window_end = ? WHERE id = ?`,
		state, toMillis(start), toMillis(end), id)
	return err
}

const eventCols = `e.id, e.calendar_id, e.account_id, e.remote_id, e.series_remote, e.ical_uid, e.title, e.start_ms, e.end_ms, e.all_day,
	e.time_zone, e.location, e.description, e.attendees, e.organizer, e.is_organizer, e.my_response, e.meeting_url, e.is_recurring,
	e.recurrence, e.rrule, e.status, e.reminders,
	(e.can_edit AND coalesce((SELECT c2.can_edit FROM calendars c2 WHERE c2.id = e.calendar_id), 0)), e.updated_ms, e.etag, e.reminders_off`

// EventRow is a stored event with its remote ids.
type EventRow struct {
	models.CalendarEvent
	RemoteID       string
	SeriesRemoteID string
	ICalUID        string
	ETag           string
}

func scanEvent(sc interface{ Scan(...any) error }) (EventRow, error) {
	var e EventRow
	var start, end, updated int64
	var allDay, isOrg, isRec, canEdit, remOff int
	var atts, org, rem string
	err := sc.Scan(&e.ID, &e.CalendarID, &e.AccountID, &e.RemoteID, &e.SeriesRemoteID, &e.ICalUID, &e.Title, &start, &end, &allDay,
		&e.TimeZone, &e.Location, &e.Description, &atts, &org, &isOrg, &e.MyResponse, &e.MeetingURL, &isRec,
		&e.Recurrence, &e.RRule, &e.Status, &rem, &canEdit, &updated, &e.ETag, &remOff)
	if err != nil {
		return e, err
	}
	e.Start, e.End, e.UpdatedAt = fromMillis(start), fromMillis(end), fromMillis(updated)
	e.AllDay, e.IsOrganizer, e.IsRecurring, e.CanEdit, e.RemindersOff = allDay == 1, isOrg == 1, isRec == 1, canEdit == 1, remOff == 1
	e.Attendees, e.Reminders = []models.Attendee{}, []int{}
	if atts != "" {
		_ = json.Unmarshal([]byte(atts), &e.Attendees)
	}
	if org != "" {
		_ = json.Unmarshal([]byte(org), &e.Organizer)
	}
	if rem != "" {
		_ = json.Unmarshal([]byte(rem), &e.Reminders)
	}
	if e.SeriesRemoteID != "" {
		e.SeriesID = SeriesID(e.CalendarID, e.SeriesRemoteID)
	}
	return e, nil
}

// SeriesID is the local id shared by the occurrences of a series.
func SeriesID(calendarID, seriesRemoteID string) string {
	return hashID("s", calendarID, seriesRemoteID)
}

// EventChanges is one batch of event changes for a calendar.
type EventChanges struct {
	Upserts []calendar.Event
	Deleted []string // remote ids
	// Replace, when set, deletes the calendar's events overlapping
	// [Start, End) that are not in Upserts (a full window fetch).
	Replace    bool
	Start, End time.Time
}

// ApplyEvents stores changes for one calendar and returns the local ids
// that were upserted.
func (s *Store) ApplyEvents(cal CalendarRow, ch EventChanges) ([]string, error) {
	var ids []string
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		up, err := tx.Prepare(`INSERT INTO events(id, calendar_id, account_id, remote_id, series_remote, ical_uid, title, start_ms, end_ms,
			all_day, time_zone, location, description, attendees, organizer, is_organizer, my_response, meeting_url, is_recurring,
			recurrence, rrule, status, reminders, can_edit, updated_ms, etag)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET series_remote = excluded.series_remote, ical_uid = excluded.ical_uid, title = excluded.title,
				start_ms = excluded.start_ms, end_ms = excluded.end_ms, all_day = excluded.all_day, time_zone = excluded.time_zone,
				location = excluded.location, description = excluded.description, attendees = excluded.attendees,
				organizer = excluded.organizer, is_organizer = excluded.is_organizer, my_response = excluded.my_response,
				meeting_url = excluded.meeting_url, is_recurring = excluded.is_recurring,
				recurrence = CASE WHEN excluded.recurrence != '' THEN excluded.recurrence ELSE events.recurrence END,
				rrule = CASE WHEN excluded.rrule != '' THEN excluded.rrule ELSE events.rrule END,
				status = excluded.status, reminders = excluded.reminders, can_edit = excluded.can_edit, updated_ms = excluded.updated_ms,
				etag = excluded.etag`)
		if err != nil {
			return err
		}
		defer up.Close()
		keep := map[string]bool{}
		for _, ev := range ch.Upserts {
			id := EventID(cal.ID, ev.RemoteID)
			keep[id] = true
			ids = append(ids, id)
			atts, _ := json.Marshal(nonNilAttendees(ev.Attendees))
			org, _ := json.Marshal(ev.Organizer)
			rem, _ := json.Marshal(nonNilInts(ev.Reminders))
			if _, err := up.Exec(id, cal.ID, cal.AccountID, ev.RemoteID, ev.SeriesRemoteID, ev.ICalUID, ev.Title, toMillis(ev.Start),
				toMillis(ev.End), boolInt(ev.AllDay), ev.TimeZone, ev.Location, ev.Description, string(atts), string(org),
				boolInt(ev.IsOrganizer), ev.MyResponse, ev.MeetingURL, boolInt(ev.IsRecurring), ev.Recurrence, ev.RRule, ev.Status,
				string(rem), boolInt(ev.CanEdit), toMillis(ev.Updated), ev.ETag); err != nil {
				return err
			}
		}
		for _, rid := range ch.Deleted {
			if _, err := tx.Exec(`DELETE FROM events WHERE id = ?`, EventID(cal.ID, rid)); err != nil {
				return err
			}
		}
		if ch.Replace {
			rows, err := tx.Query(`SELECT id FROM events WHERE calendar_id = ? AND start_ms < ? AND end_ms > ?`,
				cal.ID, toMillis(ch.End), toMillis(ch.Start))
			if err != nil {
				return err
			}
			var gone []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil && !keep[id] {
					gone = append(gone, id)
				}
			}
			rows.Close()
			for _, id := range gone {
				if _, err := tx.Exec(`DELETE FROM events WHERE id = ?`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return ids, err
}

func nonNilAttendees(a []models.Attendee) []models.Attendee {
	if a == nil {
		return []models.Attendee{}
	}
	return a
}

func nonNilInts(a []int) []int {
	if a == nil {
		return []int{}
	}
	return a
}

// Events lists events overlapping [start, end) of visible calendars of
// enabled accounts with calendar access, ordered by start. Declined invitations
// are left out unless showDeclined.
func (s *Store) Events(start, end time.Time, showDeclined bool) ([]models.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN accounts a ON a.id = c.account_id
		WHERE c.visible = 1 AND a.calendar_consent = 1 AND a.calendar_enabled = 1 AND e.status != 'cancelled'
			AND e.start_ms < ? AND e.end_ms > ? AND (? OR e.my_response != 'declined')
		ORDER BY e.start_ms, e.end_ms DESC, e.id`, toMillis(end), toMillis(start), showDeclined)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CalendarEvent{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e.CalendarEvent)
	}
	return out, rows.Err()
}

// ReminderEvents lists the events overlapping [start, end) that may show
// reminders: accounts with access, enabled and not muted, any calendar
// visibility, not cancelled or declined.
func (s *Store) ReminderEvents(start, end time.Time) ([]models.CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN accounts a ON a.id = c.account_id
		WHERE a.calendar_consent = 1 AND a.calendar_enabled = 1 AND a.calendar_muted = 0 AND e.status != 'cancelled'
			AND e.my_response != 'declined' AND e.start_ms < ? AND e.end_ms > ?
		ORDER BY e.start_ms`, toMillis(end), toMillis(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CalendarEvent{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e.CalendarEvent)
	}
	return out, rows.Err()
}

// Event returns one event.
func (s *Store) Event(id string) (EventRow, error) {
	e, err := scanEvent(s.db.QueryRow(`SELECT `+eventCols+` FROM events e WHERE e.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// SeriesEvents returns the stored occurrences of a series.
func (s *Store) SeriesEvents(calendarID, seriesRemoteID string) ([]EventRow, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events e WHERE e.calendar_id = ? AND (e.series_remote = ? OR e.remote_id = ?)`,
		calendarID, seriesRemoteID, seriesRemoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventsByICalUID finds synced events of an invitation.
func (s *Store) EventsByICalUID(accountID, uid string) ([]EventRow, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events e WHERE e.ical_uid = ? AND e.account_id = ? ORDER BY e.start_ms`, uid, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEvents removes local events by id.
func (s *Store) DeleteEvents(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM events WHERE id IN (`+placeholders(len(ids))+`)`, anySlice(ids)...)
		return err
	})
}

// PutEventRows restores events exactly (reverting optimistic changes).
func (s *Store) PutEventRows(rows []EventRow) error {
	byCal := map[string][]calendar.Event{}
	for _, r := range rows {
		byCal[r.CalendarID] = append(byCal[r.CalendarID], rowToProvider(r))
	}
	for calID, evs := range byCal {
		cal, err := s.Calendar(calID)
		if err != nil {
			continue
		}
		if _, err := s.ApplyEvents(cal, EventChanges{Upserts: evs}); err != nil {
			return err
		}
	}
	return nil
}

func rowToProvider(r EventRow) calendar.Event {
	e := r.CalendarEvent
	return calendar.Event{RemoteID: r.RemoteID, SeriesRemoteID: r.SeriesRemoteID, ICalUID: r.ICalUID, Title: e.Title,
		Start: e.Start, End: e.End, AllDay: e.AllDay, TimeZone: e.TimeZone, Location: e.Location, Description: e.Description,
		Attendees: e.Attendees, Organizer: e.Organizer, IsOrganizer: e.IsOrganizer, MyResponse: e.MyResponse, MeetingURL: e.MeetingURL,
		IsRecurring: e.IsRecurring, Recurrence: e.Recurrence, RRule: e.RRule, Status: e.Status, Reminders: e.Reminders,
		CanEdit: e.CanEdit, Updated: e.UpdatedAt, ETag: r.ETag}
}

// SetRemindersOff marks events whose reminders you turned off in gomail
// (or clears the mark). Syncs keep it.
func (s *Store) SetRemindersOff(ids []string, off bool) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE events SET reminders_off = ? WHERE id = ?`, boolInt(off), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ToProvider converts a stored event to the provider form.
func (r EventRow) ToProvider() calendar.Event { return rowToProvider(r) }

// DeleteAccountCalendars removes an account's calendars and events (when
// calendar access is withdrawn).
func (s *Store) DeleteAccountCalendars(accountID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM calendars WHERE account_id = ?`, accountID)
		return err
	})
}
