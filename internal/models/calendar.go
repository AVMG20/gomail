package models

import "time"

// Calendar is one calendar of an account (Google calendarList entry,
// Outlook calendar).
type Calendar struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	RemoteID  string `json:"remoteId"`
	Name      string `json:"name"`
	Color     string `json:"color"` // hex
	Primary   bool   `json:"primary"`
	CanEdit   bool   `json:"canEdit"` // owner/writer: events can be created and changed
	Visible   bool   `json:"visible"` // shown in the unified view (SetCalendarVisible)
	// VideoCallProvider is what EventInput.addVideoCall creates here:
	// "meet" (Google Meet), "teams" (Microsoft Teams), "skype" (personal
	// Microsoft accounts that only allow Skype) or "" (not supported).
	VideoCallProvider string `json:"videoCallProvider"`
}

// Attendee responses (also used for CalendarEvent.myResponse).
const (
	ResponseNone      = "none" // not answered yet / needs action
	ResponseAccepted  = "accepted"
	ResponseTentative = "tentative"
	ResponseDeclined  = "declined"
	ResponseOrganizer = "organizer"
)

// Attendee is an event participant.
type Attendee struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Response string `json:"response"` // none | accepted | tentative | declined | organizer
	Optional bool   `json:"optional"`
}

// Event statuses.
const (
	EventConfirmed = "confirmed"
	EventTentative = "tentative"
	EventCancelled = "cancelled"
)

// CalendarEvent is one event or one occurrence of a recurring series.
//
// Times are UTC. All-day events have AllDay set and Start/End at 00:00 UTC
// of their dates, with End exclusive (a one-day event on Oct 20 has Start
// 2026-10-20 and End 2026-10-21): read the date, ignore the time.
type CalendarEvent struct {
	ID          string     `json:"id"`
	CalendarID  string     `json:"calendarId"`
	AccountID   string     `json:"accountId"`
	Title       string     `json:"title"`
	Start       time.Time  `json:"start"`
	End         time.Time  `json:"end"`
	AllDay      bool       `json:"allDay"`
	TimeZone    string     `json:"timeZone"` // IANA zone the event was created in ("" = unknown/UTC)
	Location    string     `json:"location"`
	Description string     `json:"description"` // plain text
	Attendees   []Attendee `json:"attendees"`
	Organizer   Address    `json:"organizer"`
	IsOrganizer bool       `json:"isOrganizer"`
	MyResponse  string     `json:"myResponse"` // see Response*; "organizer" for own events
	MeetingURL  string     `json:"meetingUrl"` // https join link (Meet, Teams, ...) or ""
	IsRecurring bool       `json:"isRecurring"`
	SeriesID    string     `json:"seriesId"`   // local id shared by all occurrences of a series ("" if single)
	Recurrence  string     `json:"recurrence"` // human summary, e.g. "Weekly on Monday"; "" if single
	RRule       string     `json:"rrule"`      // RRULE when known (always for series created in gomail)
	Status      string     `json:"status"`     // confirmed | tentative | cancelled
	Reminders   []int      `json:"reminders"`  // minutes before start
	// RemindersOff: you chose "No reminder" in gomail, so the default
	// reminder (Settings) doesn't apply to this event either.
	RemindersOff bool      `json:"remindersOff"`
	CanEdit      bool      `json:"canEdit"` // the calendar is writable and we may change this event
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Recurrence frequencies for EventInput.
const (
	RepeatNone    = "none"
	RepeatDaily   = "daily"
	RepeatWeekly  = "weekly"
	RepeatMonthly = "monthly"
	RepeatYearly  = "yearly"
)

// EventRecurrence describes a simple repeating rule.
type EventRecurrence struct {
	Frequency string     `json:"frequency"` // none | daily | weekly | monthly | yearly
	Until     *time.Time `json:"until"`     // last day (inclusive), or null
	Count     int        `json:"count"`     // number of occurrences, 0 = unlimited (or Until)
}

// EventInput creates or updates an event. Start/End follow CalendarEvent
// (all-day: dates at 00:00 UTC, End exclusive; a zero End means one hour,
// or one day for all-day events).
type EventInput struct {
	AccountID       string          `json:"accountId"`
	CalendarID      string          `json:"calendarId"` // "" = the account's primary calendar
	Title           string          `json:"title"`
	Start           time.Time       `json:"start"`
	End             time.Time       `json:"end"`
	AllDay          bool            `json:"allDay"`
	TimeZone        string          `json:"timeZone"` // IANA, e.g. "Europe/Amsterdam"; "" = UTC
	Location        string          `json:"location"`
	Description     string          `json:"description"`
	Attendees       []Attendee      `json:"attendees"` // invitations are sent by the provider
	Recurrence      EventRecurrence `json:"recurrence"`
	ReminderMinutes []int           `json:"reminderMinutes"` // nil = calendar default
	// AddVideoCall creates a meeting link with the calendar's
	// videoCallProvider (Meet/Teams); an existing one is kept.
	AddVideoCall bool `json:"addVideoCall"`
	// MeetingURL is a link the user typed (Zoom, Webex, ...). Providers have
	// no field for it: it goes into the location when that is empty, else
	// "Join: <url>" is put in front of the description.
	MeetingURL string `json:"meetingUrl"`
	// RemoveMeetingURL removes the event's current meeting link (UpdateEvent):
	// a typed link is taken out of the location/description, a Google Meet
	// conference is detached. Teams meetings can't be removed (Microsoft
	// keeps them once created). With MeetingURL set, the link is replaced.
	RemoveMeetingURL bool `json:"removeMeetingUrl"`
}

// Event change scopes for UpdateEvent / DeleteEvent.
const (
	ScopeThis   = "this"
	ScopeSeries = "series"
)

// Invite is the calendar invitation (text/calendar part) of a message.
type Invite struct {
	UID       string    `json:"uid"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	AllDay    bool      `json:"allDay"`
	Location  string    `json:"location"`
	Organizer Address   `json:"organizer"`
	Method    string    `json:"method"`  // REQUEST | CANCEL | REPLY | PUBLISH
	EventID   string    `json:"eventId"` // the matching synced event ("" if not in a synced calendar): RespondToEvent works on it
}

// CalendarChanged is the payload of calendar:changed.
type CalendarChanged struct {
	AccountID string `json:"accountId"` // "" = several accounts
}

// CalendarOpen is the payload of calendar:open.
type CalendarOpen struct {
	EventID string `json:"eventId"`
}

// MoveCheck tells the UI what moving an event to another calendar does,
// so it can ask for confirmation first.
type MoveCheck struct {
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason"`       // why not, when !allowed
	CrossAccount bool   `json:"crossAccount"` // the event is recreated in another account
	HasAttendees bool   `json:"hasAttendees"` // with crossAccount (or Outlook): attendees get a cancellation and a new invitation
	Recurring    bool   `json:"recurring"`    // the whole series moves
}

// EventDraft is an event described in natural language, as parsed by the AI
// model (ParseEventText). Dates and times are local wall-clock values; empty
// fields were not mentioned.
type EventDraft struct {
	Title       string   `json:"title"`
	Date        string   `json:"date"`      // YYYY-MM-DD
	StartTime   string   `json:"startTime"` // HH:MM (24-hour)
	EndDate     string   `json:"endDate"`
	EndTime     string   `json:"endTime"`
	AllDay      bool     `json:"allDay"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Attendees   []string `json:"attendees"` // names or email addresses
	VideoCall   bool     `json:"videoCall"`
	Repeat      string   `json:"repeat"` // none | daily | weekly | monthly | yearly
}
