// Package gcal is a lean Google Calendar API v3 client implementing
// calendar.Provider.
package gcal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
)

// BaseURL of the Calendar API.
const BaseURL = "https://www.googleapis.com/calendar/v3"

// Client talks to one Google account's calendars.
type Client struct {
	api *httpx.Client
}

// New creates a client; base "" means production.
func New(tokens httpx.TokenSource, base string) *Client {
	if base == "" {
		base = BaseURL
	}
	return &Client{api: &httpx.Client{BaseURL: base, Tokens: tokens, Service: "Google Calendar"}}
}

// SetSleep replaces the backoff sleeper (tests).
func (c *Client) SetSleep(f func(context.Context, time.Duration) error) { c.api.Sleep = f }

// wrap maps "insufficient scopes" to calendar.ErrNoConsent.
func wrap(err error) error {
	if httpx.StatusOf(err) == http.StatusPreconditionFailed {
		return fmt.Errorf("%w: %v", calendar.ErrConflict, err)
	}
	var e *httpx.Error
	if errors.As(err, &e) && e.Status == http.StatusForbidden &&
		(e.Reason == "insufficientPermissions" || e.Code == "PERMISSION_DENIED" || strings.Contains(strings.ToLower(e.Message), "insufficient authentication scopes")) {
		return fmt.Errorf("%w: %v", calendar.ErrNoConsent, err)
	}
	return err
}

type gReminder struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}

func (c *Client) Calendars(ctx context.Context) ([]calendar.Calendar, error) {
	var out []calendar.Calendar
	page := ""
	for i := 0; i < 20; i++ {
		v := url.Values{"maxResults": {"250"}, "minAccessRole": {"reader"}}
		if page != "" {
			v.Set("pageToken", page)
		}
		var resp struct {
			Items []struct {
				ID               string      `json:"id"`
				Summary          string      `json:"summary"`
				SummaryOverride  string      `json:"summaryOverride"`
				BackgroundColor  string      `json:"backgroundColor"`
				Primary          bool        `json:"primary"`
				AccessRole       string      `json:"accessRole"`
				TimeZone         string      `json:"timeZone"`
				Deleted          bool        `json:"deleted"`
				DefaultReminders []gReminder `json:"defaultReminders"`
				Conference       struct {
					Allowed []string `json:"allowedConferenceSolutionTypes"`
				} `json:"conferenceProperties"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, "users/me/calendarList?"+v.Encode(), nil, &resp); err != nil {
			return nil, wrap(err)
		}
		for _, it := range resp.Items {
			if it.Deleted {
				continue
			}
			name := it.SummaryOverride
			if name == "" {
				name = it.Summary
			}
			cal := calendar.Calendar{RemoteID: it.ID, Name: name, Color: it.BackgroundColor, Primary: it.Primary,
				CanEdit: it.AccessRole == "owner" || it.AccessRole == "writer", TimeZone: calendar.IANAZone(it.TimeZone)}
			for _, t := range it.Conference.Allowed {
				if t == "hangoutsMeet" {
					cal.VideoCallProvider = "meet"
				}
			}
			for _, r := range it.DefaultReminders {
				if r.Method == "popup" || r.Method == "" {
					cal.DefaultReminders = append(cal.DefaultReminders, r.Minutes)
				}
			}
			out = append(out, cal)
		}
		if resp.NextPageToken == "" {
			break
		}
		page = resp.NextPageToken
	}
	return out, nil
}

type gTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type gPerson struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
}

type gEvent struct {
	ID               string    `json:"id,omitempty"`
	Status           string    `json:"status,omitempty"`
	Summary          string    `json:"summary"`
	Description      string    `json:"description"`
	Location         string    `json:"location"`
	Start            gTime     `json:"start"`
	End              gTime     `json:"end"`
	RecurringEventID string    `json:"recurringEventId,omitempty"`
	Recurrence       []string  `json:"recurrence,omitempty"`
	ICalUID          string    `json:"iCalUID,omitempty"`
	Organizer        *gPerson  `json:"organizer,omitempty"`
	Attendees        []gPerson `json:"attendees,omitempty"`
	HangoutLink      string    `json:"hangoutLink,omitempty"`
	ConferenceData   *struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData,omitempty"`
	Reminders *struct {
		UseDefault bool        `json:"useDefault"`
		Overrides  []gReminder `json:"overrides,omitempty"`
	} `json:"reminders,omitempty"`
	Updated         string `json:"updated,omitempty"`
	GuestsCanModify bool   `json:"guestsCanModify,omitempty"`
	Etag            string `json:"etag,omitempty"`
}

// parseTime reads a Google start/end. All-day dates become 00:00 UTC.
func parseTime(t gTime, fallbackTZ string) (time.Time, bool, string) {
	if t.Date != "" {
		d, err := time.Parse("2006-01-02", t.Date)
		if err != nil {
			return time.Time{}, true, ""
		}
		return d, true, calendar.IANAZone(t.TimeZone)
	}
	ts, err := time.Parse(time.RFC3339, t.DateTime)
	if err != nil {
		return time.Time{}, false, ""
	}
	tz := calendar.IANAZone(t.TimeZone)
	if tz == "" {
		tz = fallbackTZ
	}
	return ts.UTC(), false, tz
}

// ruleZone is the zone an RRULE's UTC UNTIL is read in: the event's, or
// the calendar's for all-day events (which have none).
func ruleZone(ev calendar.Event, cal calendar.Calendar) string {
	if ev.TimeZone != "" {
		return ev.TimeZone
	}
	return cal.TimeZone
}

var gResponse = map[string]string{"needsAction": models.ResponseNone, "accepted": models.ResponseAccepted,
	"tentative": models.ResponseTentative, "declined": models.ResponseDeclined}

// gStatus maps known answers back to Google's (unanswered: omitted).
var gStatus = map[string]string{models.ResponseAccepted: "accepted", models.ResponseTentative: "tentative",
	models.ResponseDeclined: "declined", models.ResponseOrganizer: "accepted"}

func safeURL(u string) string {
	if p, err := url.Parse(u); err == nil && p.Scheme == "https" && p.Host != "" {
		return u
	}
	return ""
}

// plainText turns a Google description (which may be HTML) into text.
func plainText(s string) string {
	if strings.ContainsAny(s, "<&") {
		s = mailmime.HTMLToText(s)
	}
	if len(s) > 64<<10 {
		s = s[:64<<10]
	}
	return s
}

func (c *Client) toEvent(g gEvent, cal calendar.Calendar) calendar.Event {
	ev := calendar.Event{RemoteID: g.ID, SeriesRemoteID: g.RecurringEventID, ICalUID: g.ICalUID, Title: g.Summary,
		Location: g.Location, Description: plainText(g.Description), Status: g.Status, MeetingURL: safeURL(g.HangoutLink)}
	if ev.Status == "" {
		ev.Status = models.EventConfirmed
	}
	ev.Start, ev.AllDay, ev.TimeZone = parseTime(g.Start, cal.TimeZone)
	ev.End, _, _ = parseTime(g.End, cal.TimeZone)
	if ev.End.Before(ev.Start) {
		ev.End = ev.Start
	}
	if ev.MeetingURL == "" && g.ConferenceData != nil {
		for _, ep := range g.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				ev.MeetingURL = safeURL(ep.URI)
				break
			}
		}
	}
	if g.Organizer != nil {
		ev.Organizer = models.Address{Name: g.Organizer.DisplayName, Email: g.Organizer.Email}
		ev.IsOrganizer = g.Organizer.Self
	}
	ev.MyResponse = models.ResponseNone
	if ev.IsOrganizer || len(g.Attendees) == 0 {
		ev.MyResponse = models.ResponseOrganizer
	}
	for _, a := range g.Attendees {
		r := gResponse[a.ResponseStatus]
		if r == "" {
			r = models.ResponseNone
		}
		if a.Organizer {
			r = models.ResponseOrganizer
		}
		ev.Attendees = append(ev.Attendees, models.Attendee{Name: a.DisplayName, Email: a.Email, Response: r, Optional: a.Optional})
		if a.Self && !ev.IsOrganizer {
			ev.MyResponse = gResponse[a.ResponseStatus]
			if ev.MyResponse == "" {
				ev.MyResponse = models.ResponseNone
			}
		}
	}
	if g.Reminders != nil {
		if g.Reminders.UseDefault {
			ev.Reminders = append([]int(nil), cal.DefaultReminders...)
		}
		for _, r := range g.Reminders.Overrides {
			if r.Method == "popup" {
				ev.Reminders = append(ev.Reminders, r.Minutes)
			}
		}
	}
	for _, r := range g.Recurrence {
		if strings.HasPrefix(r, "RRULE:") {
			ev.RRule = strings.TrimPrefix(r, "RRULE:")
		}
	}
	ev.IsRecurring = ev.SeriesRemoteID != "" || ev.RRule != ""
	if ev.RRule != "" {
		ev.Recurrence = calendar.SummarizeIn(ev.RRule, ruleZone(ev, cal))
	}
	ev.CanEdit = cal.CanEdit && (ev.IsOrganizer || g.GuestsCanModify || g.Organizer == nil)
	ev.ETag = g.Etag
	if ev.MeetingURL == "" {
		ev.MeetingURL = calendar.DetectMeetingURL(ev.Location, ev.Description)
	}
	if t, err := time.Parse(time.RFC3339, g.Updated); err == nil {
		ev.Updated = t.UTC()
	}
	return ev
}

func (c *Client) eventsPath(cal calendar.Calendar) string {
	return "calendars/" + url.PathEscape(cal.RemoteID) + "/events"
}

// list pages through events.list. It returns the events, the next sync
// token and whether the token was rejected (410).
func (c *Client) list(ctx context.Context, cal calendar.Calendar, v url.Values) ([]gEvent, string, bool, error) {
	var out []gEvent
	page := ""
	for i := 0; i < 200; i++ {
		q := url.Values{}
		for k, vs := range v {
			q[k] = vs
		}
		if page != "" {
			q.Set("pageToken", page)
		}
		var resp struct {
			Items         []gEvent `json:"items"`
			NextPageToken string   `json:"nextPageToken"`
			NextSyncToken string   `json:"nextSyncToken"`
		}
		err := c.api.JSON(ctx, http.MethodGet, c.eventsPath(cal)+"?"+q.Encode(), nil, &resp)
		if httpx.StatusOf(err) == http.StatusGone {
			return nil, "", true, nil
		}
		if err != nil {
			return nil, "", false, wrap(err)
		}
		out = append(out, resp.Items...)
		if resp.NextPageToken == "" {
			return out, resp.NextSyncToken, false, nil
		}
		page = resp.NextPageToken
	}
	return nil, "", false, errors.New("too many pages of events")
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (c *Client) Sync(ctx context.Context, cal calendar.Calendar, req calendar.SyncRequest) (calendar.SyncResult, error) {
	if req.State != "" {
		items, token, gone, err := c.list(ctx, cal, url.Values{"syncToken": {req.State}, "singleEvents": {"true"}, "maxResults": {"2500"}})
		if err != nil {
			return calendar.SyncResult{}, err
		}
		if !gone {
			res := calendar.SyncResult{State: token}
			c.collect(ctx, cal, items, &res)
			return res, nil
		}
		// 410: the token expired; full resync below.
	}
	items, token, _, err := c.list(ctx, cal, url.Values{"singleEvents": {"true"}, "maxResults": {"2500"},
		"timeMin": {rfc(req.Start)}, "timeMax": {rfc(req.End)}})
	if err != nil {
		return calendar.SyncResult{}, err
	}
	res := calendar.SyncResult{State: token, Full: true}
	c.collect(ctx, cal, items, &res)
	return res, nil
}

// collect converts items, splits cancellations and fills recurrence
// summaries from the series masters (at most 50 per round).
func (c *Client) collect(ctx context.Context, cal calendar.Calendar, items []gEvent, res *calendar.SyncResult) {
	masters := map[string]*gEvent{}
	for _, g := range items {
		if g.Status == "cancelled" {
			res.Deleted = append(res.Deleted, g.ID)
			continue
		}
		ev := c.toEvent(g, cal)
		if ev.SeriesRemoteID != "" {
			m, ok := masters[ev.SeriesRemoteID]
			if !ok && len(masters) < 50 {
				var mg gEvent
				if err := c.api.JSON(ctx, http.MethodGet, c.eventsPath(cal)+"/"+url.PathEscape(ev.SeriesRemoteID), nil, &mg); err == nil {
					m = &mg
				}
				masters[ev.SeriesRemoteID] = m
			}
			if m != nil {
				for _, r := range m.Recurrence {
					if strings.HasPrefix(r, "RRULE:") {
						ev.RRule = strings.TrimPrefix(r, "RRULE:")
						ev.Recurrence = calendar.SummarizeIn(ev.RRule, ruleZone(ev, cal))
					}
				}
			}
		}
		res.Upserts = append(res.Upserts, ev)
	}
}

func (c *Client) Range(ctx context.Context, cal calendar.Calendar, start, end time.Time) ([]calendar.Event, error) {
	items, _, _, err := c.list(ctx, cal, url.Values{"singleEvents": {"true"}, "maxResults": {"2500"},
		"timeMin": {rfc(start)}, "timeMax": {rfc(end)}})
	if err != nil {
		return nil, err
	}
	var res calendar.SyncResult
	c.collect(ctx, cal, items, &res)
	return res.Upserts, nil
}

// body renders an event for insert/patch.
func body(ev calendar.Event) map[string]any {
	// Google renders descriptions as HTML: escape the plain text.
	b := map[string]any{"summary": ev.Title, "description": strings.ReplaceAll(html.EscapeString(ev.Description), "\n", "<br>"),
		"location": ev.Location}
	tz := ev.TimeZone
	if tz == "" {
		tz = "UTC"
	}
	if ev.AllDay {
		b["start"] = gTime{Date: ev.Start.Format("2006-01-02")}
		b["end"] = gTime{Date: ev.End.Format("2006-01-02")}
	} else {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc, tz = time.UTC, "UTC"
		}
		b["start"] = gTime{DateTime: ev.Start.In(loc).Format(time.RFC3339), TimeZone: tz}
		b["end"] = gTime{DateTime: ev.End.In(loc).Format(time.RFC3339), TimeZone: tz}
	}
	if ev.Attendees != nil {
		atts := []gPerson{}
		for _, a := range ev.Attendees {
			if a.Email != "" {
				// The list replaces Google's; without their status, guests'
				// answers would reset to needsAction.
				atts = append(atts, gPerson{Email: a.Email, DisplayName: a.Name, Optional: a.Optional,
					ResponseStatus: gStatus[a.Response]})
			}
		}
		b["attendees"] = atts
	}
	if ev.Reminders != nil {
		over := []gReminder{}
		for _, m := range ev.Reminders {
			over = append(over, gReminder{Method: "popup", Minutes: m})
		}
		b["reminders"] = map[string]any{"useDefault": false, "overrides": over}
	}
	if ev.RRule != "" {
		b["recurrence"] = []string{"RRULE:" + ev.RRule}
	}
	if ev.AddVideoCall {
		b["conferenceData"] = map[string]any{"createRequest": map[string]any{"requestId": requestID(),
			"conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}}
	} else if ev.RemoveConference {
		b["conferenceData"] = nil // with conferenceDataVersion=1: detach the Meet conference
	}
	return b
}

func requestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const sendUpdates = "?sendUpdates=all"

// writeQuery is used for insert/patch: conferenceDataVersion=1 lets
// conferenceData (Meet links) be created and kept.
const writeQuery = "?sendUpdates=all&conferenceDataVersion=1"

func (c *Client) Create(ctx context.Context, cal calendar.Calendar, ev calendar.Event) (calendar.Event, error) {
	if !cal.CanEdit {
		return calendar.Event{}, calendar.ErrReadOnly
	}
	var g gEvent
	if ev.AddVideoCall && cal.VideoCallProvider != "meet" {
		ev.AddVideoCall = false
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.eventsPath(cal)+writeQuery, body(ev), &g); err != nil {
		return calendar.Event{}, wrap(err)
	}
	return c.toEvent(g, cal), nil
}

func (c *Client) get(ctx context.Context, cal calendar.Calendar, id string) (gEvent, error) {
	var g gEvent
	err := c.api.JSON(ctx, http.MethodGet, c.eventsPath(cal)+"/"+url.PathEscape(id), nil, &g)
	return g, wrap(err)
}

func (c *Client) Update(ctx context.Context, cal calendar.Calendar, remoteID string, ev calendar.Event, scope string) (calendar.Event, error) {
	target := remoteID
	b := body(ev)
	if scope == models.ScopeSeries && ev.SeriesRemoteID != "" {
		// Shift the series by the change made to this occurrence.
		target = ev.SeriesRemoteID
		master, err := c.get(ctx, cal, target)
		if err != nil {
			return calendar.Event{}, err
		}
		mev := c.toEvent(master, cal)
		shifted := ev
		if !ev.OriginalStart.IsZero() {
			shifted.Start = mev.Start.Add(ev.Start.Sub(ev.OriginalStart))
		} else {
			shifted.Start = mev.Start
		}
		shifted.End = shifted.Start.Add(ev.End.Sub(ev.Start))
		if shifted.RRule == "" {
			shifted.RRule = mev.RRule
		}
		b = body(shifted)
	}
	var hdr map[string]string
	if target == remoteID && ev.ETag != "" {
		hdr = map[string]string{"If-Match": ev.ETag}
	}
	var g gEvent
	if err := c.api.JSONHeaders(ctx, http.MethodPatch, c.eventsPath(cal)+"/"+url.PathEscape(target)+writeQuery, hdr, b, &g); err != nil {
		return calendar.Event{}, wrap(err)
	}
	return c.toEvent(g, cal), nil
}

// Move uses events.move (the event keeps its id and guests aren't
// re-invited), then applies ev's fields.
func (c *Client) Move(ctx context.Context, from, to calendar.Calendar, remoteID string, ev calendar.Event) (calendar.Event, error) {
	if !to.CanEdit {
		return calendar.Event{}, calendar.ErrReadOnly
	}
	var moved gEvent
	if err := c.api.JSON(ctx, http.MethodPost, c.eventsPath(from)+"/"+url.PathEscape(remoteID)+"/move?"+
		url.Values{"destination": {to.RemoteID}, "sendUpdates": {"none"}}.Encode(), nil, &moved); err != nil {
		return calendar.Event{}, wrap(err)
	}
	ev.ETag = ""
	return c.Update(ctx, to, moved.ID, ev, models.ScopeThis)
}

// Delete cancels an event you organize (guests are told); an invitation is
// only removed from your calendar, without telling anyone.
func (c *Client) Delete(ctx context.Context, cal calendar.Calendar, remoteID, scope, etag string) error {
	var hdr map[string]string
	if etag != "" {
		hdr = map[string]string{"If-Match": etag}
	}
	query := sendUpdates
	if g, err := c.get(ctx, cal, remoteID); err == nil && g.Organizer != nil && !g.Organizer.Self {
		query = "?sendUpdates=none"
	}
	err := c.api.JSONHeaders(ctx, http.MethodDelete, c.eventsPath(cal)+"/"+url.PathEscape(remoteID)+query, hdr, nil, nil)
	if s := httpx.StatusOf(err); s == http.StatusGone || s == http.StatusNotFound {
		return nil // already deleted
	}
	return wrap(err)
}

func (c *Client) Respond(ctx context.Context, cal calendar.Calendar, remoteID, response string) error {
	status := map[string]string{models.ResponseAccepted: "accepted", models.ResponseTentative: "tentative", models.ResponseDeclined: "declined"}[response]
	if status == "" {
		return fmt.Errorf("unknown response %q", response)
	}
	g, err := c.get(ctx, cal, remoteID)
	if err != nil {
		return err
	}
	found := false
	for i := range g.Attendees {
		if g.Attendees[i].Self {
			g.Attendees[i].ResponseStatus, found = status, true
		}
	}
	if !found {
		return errors.New("you're not on the guest list of this event")
	}
	return wrap(c.api.JSON(ctx, http.MethodPatch, c.eventsPath(cal)+"/"+url.PathEscape(remoteID)+sendUpdates,
		map[string]any{"attendees": g.Attendees}, nil))
}

// SetReminders patches only the popup reminders: they belong to this
// calendar's copy of the event, so guests may change them too and nobody is
// notified. Other reminders (email) stay, and an event on the calendar's
// default reminders keeps them when the popups don't change.
func (c *Client) SetReminders(ctx context.Context, cal calendar.Calendar, remoteID string, minutes []int) (calendar.Event, error) {
	cur, err := c.get(ctx, cal, remoteID)
	if err != nil {
		return calendar.Event{}, err
	}
	over := []gReminder{}
	if cur.Reminders != nil {
		if cur.Reminders.UseDefault && slices.Equal(sortedInts(cal.DefaultReminders), sortedInts(minutes)) {
			return c.toEvent(cur, cal), nil
		}
		for _, r := range cur.Reminders.Overrides {
			if r.Method != "popup" {
				over = append(over, r)
			}
		}
	}
	for _, m := range minutes {
		over = append(over, gReminder{Method: "popup", Minutes: m})
	}
	var g gEvent
	if err := c.api.JSON(ctx, http.MethodPatch, c.eventsPath(cal)+"/"+url.PathEscape(remoteID)+"?sendUpdates=none",
		map[string]any{"reminders": map[string]any{"useDefault": false, "overrides": over}}, &g); err != nil {
		return calendar.Event{}, wrap(err)
	}
	return c.toEvent(g, cal), nil
}

func sortedInts(v []int) []int {
	v = slices.Clone(v)
	slices.Sort(v)
	return v
}

var _ calendar.Provider = (*Client)(nil)
