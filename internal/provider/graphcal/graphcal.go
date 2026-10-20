// Package graphcal is a lean Microsoft Graph v1.0 calendar client
// implementing calendar.Provider. Times are requested in UTC
// (Prefer: outlook.timezone="UTC"), bodies as text, ids as immutable ids.
package graphcal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
)

// BaseURL of Microsoft Graph.
const BaseURL = "https://graph.microsoft.com/v1.0"

const prefer = `IdType="ImmutableId", outlook.timezone="UTC", outlook.body-content-type="text"`

// Client talks to one Microsoft mailbox's calendars.
type Client struct {
	api *httpx.Client
}

// New creates a client; base "" means production Graph.
func New(tokens httpx.TokenSource, base string) *Client {
	if base == "" {
		base = BaseURL
	}
	return &Client{api: &httpx.Client{BaseURL: base, Tokens: tokens, Service: "Outlook Calendar",
		Headers: map[string]string{"Prefer": prefer}}}
}

// SetSleep replaces the backoff sleeper (tests).
func (c *Client) SetSleep(f func(context.Context, time.Duration) error) { c.api.Sleep = f }

func wrap(err error) error {
	if httpx.StatusOf(err) == http.StatusPreconditionFailed {
		return fmt.Errorf("%w: %v", calendar.ErrConflict, err)
	}
	var e *httpx.Error
	if errors.As(err, &e) && e.Status == http.StatusForbidden && (strings.EqualFold(e.Code, "ErrorAccessDenied") ||
		strings.Contains(strings.ToLower(e.Message), "access is denied")) {
		return fmt.Errorf("%w: %v", calendar.ErrNoConsent, err)
	}
	return err
}

// presetColors maps Graph calendarColor names to hex.
var presetColors = map[string]string{
	"lightBlue": "#8ab4f8", "lightGreen": "#81c995", "lightOrange": "#fbbc04", "lightGray": "#bdc1c6", "lightYellow": "#fdd663",
	"lightTeal": "#78d9ec", "lightPink": "#ff8bcb", "lightBrown": "#c8a588", "lightRed": "#f28b82", "maxColor": "",
}

// videoProvider picks the online meeting provider gomail can create.
func videoProvider(allowed []string) string {
	p := ""
	for _, a := range allowed {
		switch a {
		case "teamsForBusiness":
			return "teams"
		case "skypeForConsumer":
			p = "skype"
		}
	}
	return p
}

var onlineProviders = map[string]string{"teams": "teamsForBusiness", "skype": "skypeForConsumer"}

func (c *Client) Calendars(ctx context.Context) ([]calendar.Calendar, error) {
	var out []calendar.Calendar
	next := "me/calendars?$top=100&$select=id,name,color,hexColor,isDefaultCalendar,canEdit,allowedOnlineMeetingProviders"
	for i := 0; next != "" && i < 20; i++ {
		var resp struct {
			Value []struct {
				ID                string   `json:"id"`
				Name              string   `json:"name"`
				Color             string   `json:"color"`
				HexColor          string   `json:"hexColor"`
				IsDefaultCalendar bool     `json:"isDefaultCalendar"`
				CanEdit           bool     `json:"canEdit"`
				Providers         []string `json:"allowedOnlineMeetingProviders"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, next, nil, &resp); err != nil {
			return nil, wrap(err)
		}
		for _, v := range resp.Value {
			color := v.HexColor
			if color == "" {
				color = presetColors[v.Color]
			}
			out = append(out, calendar.Calendar{RemoteID: v.ID, Name: v.Name, Color: color, Primary: v.IsDefaultCalendar, CanEdit: v.CanEdit,
				VideoCallProvider: videoProvider(v.Providers)})
		}
		next = resp.NextLink
	}
	return out, nil
}

type gTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type gEmail struct {
	EmailAddress struct {
		Name    string `json:"name,omitempty"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type gRecurrence struct {
	Pattern struct {
		Type       string   `json:"type"`
		Interval   int      `json:"interval"`
		DaysOfWeek []string `json:"daysOfWeek,omitempty"`
		DayOfMonth int      `json:"dayOfMonth,omitempty"`
		Month      int      `json:"month,omitempty"`
		Index      string   `json:"index,omitempty"`
	} `json:"pattern"`
	Range struct {
		Type                string `json:"type"`
		StartDate           string `json:"startDate"`
		EndDate             string `json:"endDate,omitempty"`
		NumberOfOccurrences int    `json:"numberOfOccurrences,omitempty"`
		RecurrenceTimeZone  string `json:"recurrenceTimeZone,omitempty"`
	} `json:"range"`
}

type gEvent struct {
	ID      string `json:"id"`
	ICalUID string `json:"iCalUId"`
	Subject string `json:"subject"`
	Body    *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	Start                 gTime  `json:"start"`
	End                   gTime  `json:"end"`
	OriginalStartTimeZone string `json:"originalStartTimeZone"`
	IsAllDay              bool   `json:"isAllDay"`
	IsCancelled           bool   `json:"isCancelled"`
	IsOrganizer           bool   `json:"isOrganizer"`
	Location              *struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
	Attendees []struct {
		gEmail
		Type   string `json:"type"`
		Status *struct {
			Response string `json:"response"`
		} `json:"status"`
	} `json:"attendees"`
	Organizer      *gEmail `json:"organizer"`
	ResponseStatus *struct {
		Response string `json:"response"`
	} `json:"responseStatus"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	OnlineMeetingURL           string       `json:"onlineMeetingUrl"`
	Type                       string       `json:"type"`
	SeriesMasterID             string       `json:"seriesMasterId"`
	Recurrence                 *gRecurrence `json:"recurrence"`
	IsReminderOn               bool         `json:"isReminderOn"`
	ReminderMinutesBeforeStart int          `json:"reminderMinutesBeforeStart"`
	LastModifiedDateTime       string       `json:"lastModifiedDateTime"`
	ShowAs                     string       `json:"showAs"`
	ETag                       string       `json:"@odata.etag"`
	Removed                    *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

var gResponse = map[string]string{"none": models.ResponseNone, "notResponded": models.ResponseNone, "organizer": models.ResponseOrganizer,
	"accepted": models.ResponseAccepted, "tentativelyAccepted": models.ResponseTentative, "declined": models.ResponseDeclined}

func response(r string) string {
	if v := gResponse[r]; v != "" {
		return v
	}
	return models.ResponseNone
}

// parseUTC reads a Graph dateTime (in UTC thanks to the Prefer header).
func parseUTC(t gTime) time.Time {
	s := strings.TrimSuffix(t.DateTime, "Z")
	loc := time.UTC
	if tz := calendar.IANAZone(t.TimeZone); tz != "" && tz != "UTC" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l // only when the server ignored the Prefer header
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.0000000", "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999"} {
		if ts, err := time.ParseInLocation(layout, s, loc); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}

func safeURL(u string) string {
	if p, err := url.Parse(u); err == nil && p.Scheme == "https" && p.Host != "" {
		return u
	}
	return ""
}

var dayCodes = map[string]string{"sunday": "SU", "monday": "MO", "tuesday": "TU", "wednesday": "WE", "thursday": "TH", "friday": "FR", "saturday": "SA"}

// rruleOf translates a Graph recurrence pattern into an RRULE.
func rruleOf(r *gRecurrence) string {
	if r == nil {
		return ""
	}
	freq := map[string]string{"daily": "DAILY", "weekly": "WEEKLY", "absoluteMonthly": "MONTHLY", "relativeMonthly": "MONTHLY",
		"absoluteYearly": "YEARLY", "relativeYearly": "YEARLY"}[r.Pattern.Type]
	if freq == "" {
		return ""
	}
	parts := []string{"FREQ=" + freq}
	if r.Pattern.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Pattern.Interval))
	}
	if len(r.Pattern.DaysOfWeek) > 0 && freq == "WEEKLY" {
		var ds []string
		for _, d := range r.Pattern.DaysOfWeek {
			if c := dayCodes[strings.ToLower(d)]; c != "" {
				ds = append(ds, c)
			}
		}
		parts = append(parts, "BYDAY="+strings.Join(ds, ","))
	}
	switch r.Range.Type {
	case "numbered":
		parts = append(parts, "COUNT="+strconv.Itoa(r.Range.NumberOfOccurrences))
	case "endDate":
		if d, err := time.Parse("2006-01-02", r.Range.EndDate); err == nil {
			parts = append(parts, "UNTIL="+d.Add(24*time.Hour-time.Second).Format("20060102T150405Z"))
		}
	}
	return strings.Join(parts, ";")
}

func toEvent(g gEvent, cal calendar.Calendar) calendar.Event {
	ev := calendar.Event{RemoteID: g.ID, ICalUID: g.ICalUID, Title: g.Subject, IsOrganizer: g.IsOrganizer,
		TimeZone: calendar.IANAZone(g.OriginalStartTimeZone), Status: models.EventConfirmed}
	if g.Body != nil {
		ev.Description = g.Body.Content
		if len(ev.Description) > 64<<10 {
			ev.Description = ev.Description[:64<<10]
		}
	}
	if g.Location != nil {
		ev.Location = g.Location.DisplayName
	}
	ev.Start, ev.End = parseUTC(g.Start), parseUTC(g.End)
	if g.IsAllDay {
		ev.AllDay = true
		ev.Start, ev.End = allDayDate(ev.Start, ev.TimeZone), allDayDate(ev.End, ev.TimeZone)
		if !ev.End.After(ev.Start) {
			ev.End = ev.Start.AddDate(0, 0, 1)
		}
	}
	ev.ETag = g.ETag
	if g.IsCancelled {
		ev.Status = models.EventCancelled
	} else if g.ShowAs == "tentative" {
		ev.Status = models.EventTentative
	}
	if g.OnlineMeeting != nil {
		ev.MeetingURL = safeURL(g.OnlineMeeting.JoinURL)
	}
	if ev.MeetingURL == "" {
		ev.MeetingURL = safeURL(g.OnlineMeetingURL)
	}
	if g.Organizer != nil {
		ev.Organizer = models.Address{Name: g.Organizer.EmailAddress.Name, Email: g.Organizer.EmailAddress.Address}
	}
	for _, a := range g.Attendees {
		r := models.ResponseNone
		if a.Status != nil {
			r = response(a.Status.Response)
		}
		ev.Attendees = append(ev.Attendees, models.Attendee{Name: a.EmailAddress.Name, Email: a.EmailAddress.Address,
			Response: r, Optional: a.Type == "optional"})
	}
	switch {
	case g.IsOrganizer:
		ev.MyResponse = models.ResponseOrganizer
	case g.ResponseStatus != nil:
		ev.MyResponse = response(g.ResponseStatus.Response)
	default:
		ev.MyResponse = models.ResponseNone
	}
	if g.Type == "occurrence" || g.Type == "exception" {
		ev.SeriesRemoteID = g.SeriesMasterID
	}
	ev.RRule = rruleOf(g.Recurrence)
	ev.IsRecurring = ev.SeriesRemoteID != "" || ev.RRule != "" || g.Type == "seriesMaster"
	if ev.RRule != "" {
		ev.Recurrence = calendar.Summarize(ev.RRule)
	}
	if g.IsReminderOn {
		ev.Reminders = []int{g.ReminderMinutesBeforeStart}
	}
	ev.CanEdit = cal.CanEdit && g.IsOrganizer
	if ev.MeetingURL == "" {
		ev.MeetingURL = calendar.DetectMeetingURL(ev.Location, ev.Description)
	}
	if t, err := time.Parse(time.RFC3339, g.LastModifiedDateTime); err == nil {
		ev.Updated = t.UTC()
	}
	return ev
}

// allDayDate recovers the date of an all-day boundary. Graph reports
// all-day events at midnight of the requested zone (UTC), but some
// servers convert from the event's zone instead (e.g. 15:00Z for midnight
// in Tokyo): then the event's zone gives the date, or the nearest midnight
// when the zone is unknown. Dates never shift by a day either way.
func allDayDate(t time.Time, tz string) time.Time {
	if t.Hour() == 0 && t.Minute() == 0 {
		return calendar.DateUTC(t)
	}
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			if l := t.In(loc); l.Hour() == 0 && l.Minute() == 0 {
				return calendar.DateUTC(l)
			}
		}
	}
	return calendar.DateUTC(t.Add(12 * time.Hour))
}

func graphTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05") }

// page fetches a collection (calendarView or delta) following nextLinks.
func (c *Client) page(ctx context.Context, first string) ([]gEvent, string, error) {
	var out []gEvent
	next, delta := first, ""
	for i := 0; next != ""; i++ {
		if i > 500 {
			return nil, "", errors.New("too many pages of events")
		}
		resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet, Path: next,
			Headers: map[string]string{"Prefer": prefer + ", odata.maxpagesize=200"}})
		if err != nil {
			return nil, "", err
		}
		var pg struct {
			Value     []gEvent `json:"value"`
			NextLink  string   `json:"@odata.nextLink"`
			DeltaLink string   `json:"@odata.deltaLink"`
		}
		err = json.NewDecoder(resp.Body).Decode(&pg)
		resp.Body.Close()
		if err != nil {
			return nil, "", fmt.Errorf("unexpected calendar response: %w", err)
		}
		out = append(out, pg.Value...)
		next = pg.NextLink
		if pg.DeltaLink != "" {
			delta = pg.DeltaLink
		}
	}
	return out, delta, nil
}

func isStateGone(err error) bool {
	var e *httpx.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Status == http.StatusGone || strings.EqualFold(e.Code, "SyncStateNotFound") ||
		strings.EqualFold(e.Code, "syncStateInvalid") || strings.EqualFold(e.Code, "ErrorSyncStateInvalid")
}

func rangeQuery(start, end time.Time) string {
	return url.Values{"startDateTime": {graphTime(start)}, "endDateTime": {graphTime(end)}}.Encode()
}

// Sync uses calendarView delta for the primary calendar (the only one v1.0
// supports) and a full calendarView window fetch for the others.
func (c *Client) Sync(ctx context.Context, cal calendar.Calendar, req calendar.SyncRequest) (calendar.SyncResult, error) {
	if !cal.Primary {
		evs, err := c.Range(ctx, cal, req.Start, req.End)
		if err != nil {
			return calendar.SyncResult{}, err
		}
		return calendar.SyncResult{Upserts: evs, Full: true}, nil
	}
	full := req.State == ""
	items, delta, err := c.page(ctx, firstNonEmpty(req.State, "me/calendarView/delta?"+rangeQuery(req.Start, req.End)))
	if isStateGone(err) {
		full = true
		items, delta, err = c.page(ctx, "me/calendarView/delta?"+rangeQuery(req.Start, req.End))
	}
	if err != nil {
		return calendar.SyncResult{}, wrap(err)
	}
	res := calendar.SyncResult{State: delta, Full: full}
	c.collect(ctx, cal, items, &res)
	return res, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// collect converts items, splits removals and fills recurrence summaries
// from the series masters (at most 50 per round).
func (c *Client) collect(ctx context.Context, cal calendar.Calendar, items []gEvent, res *calendar.SyncResult) {
	masters := map[string]string{}
	for _, g := range items {
		if g.Removed != nil || g.IsCancelled {
			res.Deleted = append(res.Deleted, g.ID)
			continue
		}
		if g.Type == "seriesMaster" {
			continue // occurrences are listed individually
		}
		ev := toEvent(g, cal)
		if ev.SeriesRemoteID != "" {
			rule, ok := masters[ev.SeriesRemoteID]
			if !ok && len(masters) < 50 {
				var m gEvent
				if err := c.api.JSON(ctx, http.MethodGet, "me/events/"+url.PathEscape(ev.SeriesRemoteID)+"?$select=recurrence", nil, &m); err == nil {
					rule = rruleOf(m.Recurrence)
				}
				masters[ev.SeriesRemoteID] = rule
			}
			if rule != "" {
				ev.RRule, ev.Recurrence = rule, calendar.Summarize(rule)
			}
		}
		res.Upserts = append(res.Upserts, ev)
	}
}

func (c *Client) Range(ctx context.Context, cal calendar.Calendar, start, end time.Time) ([]calendar.Event, error) {
	items, _, err := c.page(ctx, "me/calendars/"+url.PathEscape(cal.RemoteID)+"/calendarView?"+rangeQuery(start, end)+"&$top=200")
	if err != nil {
		return nil, wrap(err)
	}
	var res calendar.SyncResult
	c.collect(ctx, cal, items, &res)
	return res.Upserts, nil
}

var graphDays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// body renders an event for create/update. Times are sent in the event's
// zone (Windows id when known) so recurrences follow its DST rules.
func body(ev calendar.Event, withRecurrence bool, videoProvider string) map[string]any {
	b := map[string]any{"subject": ev.Title, "body": map[string]string{"contentType": "text", "content": ev.Description},
		"location": map[string]string{"displayName": ev.Location}, "isAllDay": ev.AllDay}
	tzName, loc := "UTC", time.UTC
	if ev.TimeZone != "" {
		if l, err := time.LoadLocation(ev.TimeZone); err == nil {
			if w := calendar.WindowsZone(ev.TimeZone); w != "" {
				tzName, loc = w, l
			}
		}
	}
	if ev.AllDay {
		b["start"] = gTime{DateTime: ev.Start.Format("2006-01-02") + "T00:00:00", TimeZone: tzName}
		b["end"] = gTime{DateTime: ev.End.Format("2006-01-02") + "T00:00:00", TimeZone: tzName}
	} else {
		b["start"] = gTime{DateTime: ev.Start.In(loc).Format("2006-01-02T15:04:05"), TimeZone: tzName}
		b["end"] = gTime{DateTime: ev.End.In(loc).Format("2006-01-02T15:04:05"), TimeZone: tzName}
	}
	if ev.Attendees != nil {
		atts := []map[string]any{}
		for _, a := range ev.Attendees {
			if a.Email == "" {
				continue
			}
			typ := "required"
			if a.Optional {
				typ = "optional"
			}
			atts = append(atts, map[string]any{"emailAddress": map[string]string{"address": a.Email, "name": a.Name}, "type": typ})
		}
		b["attendees"] = atts
	}
	if ev.Reminders != nil {
		b["isReminderOn"] = len(ev.Reminders) > 0
		if len(ev.Reminders) > 0 {
			b["reminderMinutesBeforeStart"] = ev.Reminders[0]
		}
	}
	if p := onlineProviders[videoProvider]; ev.AddVideoCall && p != "" {
		b["isOnlineMeeting"] = true
		b["onlineMeetingProvider"] = p
	}
	if withRecurrence && ev.RRule != "" {
		if r := recurrenceOf(ev.RRule, ev.Start.In(loc), tzName); r != nil {
			b["recurrence"] = r
		}
	}
	return b
}

// recurrenceOf turns a simple RRULE (as built by calendar.BuildRRule) into
// a Graph patternedRecurrence.
func recurrenceOf(rule string, start time.Time, tz string) map[string]any {
	kv := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		k, v, _ := strings.Cut(p, "=")
		kv[k] = v
	}
	pattern := map[string]any{"interval": 1}
	switch kv["FREQ"] {
	case "DAILY":
		pattern["type"] = "daily"
	case "WEEKLY":
		pattern["type"] = "weekly"
		pattern["daysOfWeek"] = []string{graphDays[start.Weekday()]}
	case "MONTHLY":
		pattern["type"], pattern["dayOfMonth"] = "absoluteMonthly", start.Day()
	case "YEARLY":
		pattern["type"], pattern["dayOfMonth"], pattern["month"] = "absoluteYearly", start.Day(), int(start.Month())
	default:
		return nil
	}
	rng := map[string]any{"type": "noEnd", "startDate": start.Format("2006-01-02"), "recurrenceTimeZone": tz}
	if n, err := strconv.Atoi(kv["COUNT"]); err == nil && n > 0 {
		rng["type"], rng["numberOfOccurrences"] = "numbered", n
	} else if u := kv["UNTIL"]; len(u) >= 8 {
		if d, err := time.Parse("20060102", u[:8]); err == nil {
			rng["type"], rng["endDate"] = "endDate", d.Format("2006-01-02")
		}
	}
	return map[string]any{"pattern": pattern, "range": rng}
}

func (c *Client) Create(ctx context.Context, cal calendar.Calendar, ev calendar.Event) (calendar.Event, error) {
	if !cal.CanEdit {
		return calendar.Event{}, calendar.ErrReadOnly
	}
	var g gEvent
	if err := c.api.JSON(ctx, http.MethodPost, "me/calendars/"+url.PathEscape(cal.RemoteID)+"/events", body(ev, true, cal.VideoCallProvider), &g); err != nil {
		return calendar.Event{}, wrap(err)
	}
	return toEvent(g, cal), nil
}

func (c *Client) Update(ctx context.Context, cal calendar.Calendar, remoteID string, ev calendar.Event, scope string) (calendar.Event, error) {
	target := remoteID
	var b map[string]any
	if scope == models.ScopeSeries && ev.SeriesRemoteID != "" {
		target = ev.SeriesRemoteID
		var master gEvent
		if err := c.api.JSON(ctx, http.MethodGet, "me/events/"+url.PathEscape(target), nil, &master); err != nil {
			return calendar.Event{}, wrap(err)
		}
		mev := toEvent(master, cal)
		shifted := ev
		shifted.Start = mev.Start
		if !ev.OriginalStart.IsZero() {
			shifted.Start = mev.Start.Add(ev.Start.Sub(ev.OriginalStart))
		}
		shifted.End = shifted.Start.Add(ev.End.Sub(ev.Start))
		b = body(shifted, ev.RRule != "", cal.VideoCallProvider)
	} else {
		b = body(ev, ev.SeriesRemoteID == "" && ev.RRule != "", cal.VideoCallProvider)
	}
	var hdr map[string]string
	if target == remoteID && ev.ETag != "" {
		hdr = map[string]string{"If-Match": ev.ETag}
	}
	var g gEvent
	if err := c.api.JSONHeaders(ctx, http.MethodPatch, "me/events/"+url.PathEscape(target), hdr, b, &g); err != nil {
		return calendar.Event{}, wrap(err)
	}
	return toEvent(g, cal), nil
}

// Move has no Graph equivalent for events: the event is created in the
// target calendar and deleted from the source (attendees get a
// cancellation and a new invitation).
func (c *Client) Move(ctx context.Context, from, to calendar.Calendar, remoteID string, ev calendar.Event) (calendar.Event, error) {
	created, err := c.Create(ctx, to, ev)
	if err != nil {
		return calendar.Event{}, err
	}
	if err := c.Delete(ctx, from, remoteID, models.ScopeSeries, ""); err != nil {
		return created, fmt.Errorf("the event was copied, but the original couldn't be removed: %w", err)
	}
	return created, nil
}

func (c *Client) Delete(ctx context.Context, cal calendar.Calendar, remoteID, scope, etag string) error {
	var hdr map[string]string
	if etag != "" {
		hdr = map[string]string{"If-Match": etag}
	}
	err := c.api.JSONHeaders(ctx, http.MethodDelete, "me/events/"+url.PathEscape(remoteID), hdr, nil, nil)
	if httpx.StatusOf(err) == http.StatusNotFound {
		return nil
	}
	return wrap(err)
}

func (c *Client) Respond(ctx context.Context, cal calendar.Calendar, remoteID, resp string) error {
	action := map[string]string{models.ResponseAccepted: "accept", models.ResponseTentative: "tentativelyAccept", models.ResponseDeclined: "decline"}[resp]
	if action == "" {
		return fmt.Errorf("unknown response %q", resp)
	}
	return wrap(c.api.JSON(ctx, http.MethodPost, "me/events/"+url.PathEscape(remoteID)+"/"+action, map[string]any{"sendResponse": true}, nil))
}

var _ calendar.Provider = (*Client)(nil)
