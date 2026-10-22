package fakes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// CalEvent is an event in a fake calendar (both providers).
type CalEvent struct {
	ID       string
	Title    string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Attendee string // invited guest (self is the mailbox owner)
	Response string // the owner's response: "needsAction" (Google) / "none" (Graph) ...
	ICalUID  string
	// Reminders are the owner's own popup reminders (minutes before; nil = none).
	Reminders []int
}

// guestFields are what a guest may change on an invitation: their answer and
// their own reminders. The fakes refuse other changes, like the providers do
// (or worse: Google would quietly fork the guest's copy).
var guestFields = map[string]bool{"attendees": true, "reminders": true, "isReminderOn": true, "reminderMinutesBeforeStart": true}

func guestPatchOK(e *CalEvent, b map[string]any) bool {
	if e.Attendee == "" {
		return true
	}
	for k := range b {
		if !guestFields[k] {
			return false
		}
	}
	return true
}

// GoogleCalendar fakes the Calendar API v3: one primary calendar, full and
// syncToken listing, insert/patch/delete. NoScope makes every call fail
// with "insufficient authentication scopes".
type GoogleCalendar struct {
	Server *httptest.Server
	Email  string
	Auth   func(string) bool

	mu      sync.Mutex
	events  map[string]*CalEvent
	version int
	changed map[string]int // id -> version of last change
	deleted map[string]int
	n       int
	noScope bool
}

// NewGoogleCalendar starts the fake.
func NewGoogleCalendar(email string, auth func(string) bool) *GoogleCalendar {
	g := &GoogleCalendar{Email: email, Auth: auth, events: map[string]*CalEvent{}, changed: map[string]int{}, deleted: map[string]int{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

// Close stops the server. URL is its base URL.
func (g *GoogleCalendar) Close()      { g.Server.Close() }
func (g *GoogleCalendar) URL() string { return g.Server.URL }

// SetNoScope toggles the insufficient-scope failure.
func (g *GoogleCalendar) SetNoScope(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.noScope = on
}

// Add stores an event and returns its id.
func (g *GoogleCalendar) Add(e CalEvent) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.add(&e)
}

func (g *GoogleCalendar) add(e *CalEvent) string {
	g.n++
	if e.ID == "" {
		e.ID = fmt.Sprintf("gev%d", g.n)
	}
	if e.ICalUID == "" {
		e.ICalUID = e.ID + "@google.com"
	}
	g.events[e.ID] = e
	g.version++
	g.changed[e.ID] = g.version
	return e.ID
}

func (g *GoogleCalendar) etag(id string) string { return fmt.Sprintf(`"%d"`, g.changed[id]) }

// Touch changes an event's title on the server (a concurrent edit
// elsewhere), giving it a new etag.
func (g *GoogleCalendar) Touch(id, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.events[id]; ok {
		e.Title = title
		g.version++
		g.changed[id] = g.version
	}
}

// Event returns a copy of an event.
func (g *GoogleCalendar) Event(id string) (CalEvent, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.events[id]
	if !ok {
		return CalEvent{}, false
	}
	return *e, true
}

func (g *GoogleCalendar) render(e *CalEvent) map[string]any {
	m := map[string]any{"id": e.ID, "status": "confirmed", "summary": e.Title, "iCalUID": e.ICalUID,
		"organizer": map[string]any{"email": g.Email, "self": e.Attendee == ""}, "etag": g.etag(e.ID)}
	if e.AllDay {
		m["start"], m["end"] = map[string]string{"date": e.Start.Format("2006-01-02")}, map[string]string{"date": e.End.Format("2006-01-02")}
	} else {
		m["start"] = map[string]string{"dateTime": e.Start.Format(time.RFC3339), "timeZone": "UTC"}
		m["end"] = map[string]string{"dateTime": e.End.Format(time.RFC3339), "timeZone": "UTC"}
	}
	if e.Attendee != "" {
		m["organizer"] = map[string]any{"email": e.Attendee}
		m["attendees"] = []any{map[string]any{"email": e.Attendee, "organizer": true, "responseStatus": "accepted"},
			map[string]any{"email": g.Email, "self": true, "responseStatus": e.Response}}
	}
	over := []any{}
	for _, r := range e.Reminders {
		over = append(over, map[string]any{"method": "popup", "minutes": r})
	}
	m["reminders"] = map[string]any{"useDefault": false, "overrides": over}
	return m
}

func (g *GoogleCalendar) serve(w http.ResponseWriter, r *http.Request) {
	if g.Auth != nil && !g.Auth(bearer(r)) {
		writeJSON(w, 401, map[string]any{"error": map[string]any{"code": 401, "message": "Invalid Credentials"}})
		return
	}
	var body map[string]any
	if data, _ := io.ReadAll(r.Body); len(data) > 0 {
		_ = json.Unmarshal(data, &body)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.noScope {
		writeJSON(w, 403, map[string]any{"error": map[string]any{"code": 403, "message": "Request had insufficient authentication scopes.",
			"status": "PERMISSION_DENIED", "errors": []any{map[string]any{"reason": "insufficientPermissions"}}}})
		return
	}
	q := r.URL.Query()
	evPath := "/calendars/primary-cal/events"
	switch {
	case r.URL.Path == "/users/me/calendarList":
		writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"id": "primary-cal", "summary": g.Email, "primary": true,
			"accessRole": "owner", "backgroundColor": "#4285f4", "timeZone": "UTC"}}})
	case r.URL.Path == evPath && r.Method == http.MethodGet:
		items := []any{}
		if tok := q.Get("syncToken"); tok != "" {
			var since int
			if _, err := fmt.Sscanf(tok, "v%d", &since); err != nil {
				writeJSON(w, 410, map[string]any{"error": map[string]any{"code": 410, "message": "Sync token is no longer valid"}})
				return
			}
			for id, v := range g.changed {
				if e, ok := g.events[id]; ok && v > since {
					items = append(items, g.render(e))
				}
			}
			for id, v := range g.deleted {
				if v > since {
					items = append(items, map[string]any{"id": id, "status": "cancelled"})
				}
			}
		} else {
			min, _ := time.Parse(time.RFC3339, q.Get("timeMin"))
			max, _ := time.Parse(time.RFC3339, q.Get("timeMax"))
			var list []*CalEvent
			for _, e := range g.events {
				if e.End.After(min) && (max.IsZero() || e.Start.Before(max)) {
					list = append(list, e)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
			for _, e := range list {
				items = append(items, g.render(e))
			}
		}
		writeJSON(w, 200, map[string]any{"items": items, "nextSyncToken": fmt.Sprintf("v%d", g.version)})
	case r.URL.Path == evPath && r.Method == http.MethodPost:
		e := &CalEvent{}
		applyGoogle(e, body)
		g.add(e)
		writeJSON(w, 200, g.render(e))
	case strings.HasPrefix(r.URL.Path, evPath+"/"):
		id := strings.TrimPrefix(r.URL.Path, evPath+"/")
		e, ok := g.events[id]
		if !ok {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "message": "Not Found"}})
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && m != g.etag(id) && r.Method != http.MethodGet {
			writeJSON(w, 412, map[string]any{"error": map[string]any{"code": 412, "message": "Precondition Failed"}})
			return
		}
		if strings.HasSuffix(id, "/move") {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "message": "single calendar fake"}})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, g.render(e))
		case http.MethodPatch:
			if !guestPatchOK(e, body) {
				writeJSON(w, 403, map[string]any{"error": map[string]any{"code": 403, "message": "Forbidden: only the organizer can change this event"}})
				return
			}
			applyGoogle(e, body)
			g.version++
			g.changed[id] = g.version
			writeJSON(w, 200, g.render(e))
		case http.MethodDelete:
			delete(g.events, id)
			g.version++
			g.deleted[id] = g.version
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": 404, "message": "fake calendar: no route " + r.URL.Path}})
	}
}

func applyGoogle(e *CalEvent, b map[string]any) {
	if v, ok := b["summary"].(string); ok {
		e.Title = v
	}
	parse := func(v any) (time.Time, bool) {
		m, _ := v.(map[string]any)
		if d, ok := m["date"].(string); ok {
			t, _ := time.Parse("2006-01-02", d)
			return t, true
		}
		t, _ := time.Parse(time.RFC3339, fmt.Sprint(m["dateTime"]))
		return t.UTC(), false
	}
	if v, ok := b["start"]; ok {
		e.Start, e.AllDay = parse(v)
	}
	if v, ok := b["end"]; ok {
		e.End, _ = parse(v)
	}
	if atts, ok := b["attendees"].([]any); ok {
		for _, a := range atts {
			m, _ := a.(map[string]any)
			if m["self"] == true {
				e.Response = fmt.Sprint(m["responseStatus"])
			}
		}
	}
	if r, ok := b["reminders"].(map[string]any); ok {
		e.Reminders = nil
		over, _ := r["overrides"].([]any)
		for _, o := range over {
			m, _ := o.(map[string]any)
			if n, ok := m["minutes"].(float64); ok {
				e.Reminders = append(e.Reminders, int(n))
			}
		}
	}
}

// ---- Graph calendar routes (served by Graph) ----

// AddCalEvent adds an event to the fake mailbox's default calendar.
func (g *Graph) AddCalEvent(e CalEvent) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addCal(&e)
}

func (g *Graph) addCal(e *CalEvent) string {
	g.n++
	if e.ID == "" {
		e.ID = fmt.Sprintf("AAMkE-%04d", g.n)
	}
	if e.ICalUID == "" {
		e.ICalUID = e.ID + "@outlook"
	}
	if g.calEvents == nil {
		g.calEvents = map[string]*CalEvent{}
	}
	g.calEvents[e.ID] = e
	return e.ID
}

// CalEvent returns a copy of a calendar event.
func (g *Graph) CalEvent(id string) (CalEvent, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.calEvents[id]
	if !ok {
		return CalEvent{}, false
	}
	return *e, true
}

func (g *Graph) renderCal(e *CalEvent) map[string]any {
	f := func(t time.Time) map[string]string {
		return map[string]string{"dateTime": t.UTC().Format("2006-01-02T15:04:05.0000000"), "timeZone": "UTC"}
	}
	m := map[string]any{"id": e.ID, "iCalUId": e.ICalUID, "subject": e.Title, "start": f(e.Start), "end": f(e.End), "isAllDay": e.AllDay,
		"type": "singleInstance", "isOrganizer": e.Attendee == "", "originalStartTimeZone": "W. Europe Standard Time",
		"organizer": map[string]any{"emailAddress": map[string]string{"address": g.Email}}}
	if e.Attendee != "" {
		m["organizer"] = map[string]any{"emailAddress": map[string]string{"address": e.Attendee}}
		m["responseStatus"] = map[string]string{"response": e.Response}
	}
	if len(e.Reminders) > 0 {
		m["isReminderOn"], m["reminderMinutesBeforeStart"] = true, e.Reminders[0]
	}
	return m
}

// calendarRoute serves /me/calendars, calendarView (delta) and events.
// It reports whether it handled the request (the caller holds g.mu).
func (g *Graph) calendarRoute(w http.ResponseWriter, r *http.Request, p string, body map[string]any) bool {
	q := r.URL.Query()
	seg := strings.Split(p, "/")
	switch {
	case p == "me/calendars":
		writeJSON(w, 200, map[string]any{"value": []any{map[string]any{"id": "cal-default", "name": "Calendar", "color": "lightBlue",
			"isDefaultCalendar": true, "canEdit": true}}})
	case p == "me/calendarView/delta":
		var list []*CalEvent
		for _, e := range g.calEvents {
			list = append(list, e)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
		out := []any{}
		for _, e := range list {
			out = append(out, g.renderCal(e))
		}
		for _, id := range g.calRemoved {
			out = append(out, map[string]any{"id": id, "@removed": map[string]string{"reason": "deleted"}})
		}
		g.calRemoved = nil
		_ = q
		writeJSON(w, 200, map[string]any{"value": out, "@odata.deltaLink": g.URL() + "/me/calendarView/delta?$deltatoken=x"})
	case len(seg) == 4 && seg[1] == "calendars" && seg[3] == "events" && r.Method == http.MethodPost:
		e := &CalEvent{}
		g.applyCal(e, body)
		g.addCal(e)
		writeJSON(w, 201, g.renderCal(e))
	case len(seg) >= 3 && seg[1] == "events":
		e, ok := g.calEvents[seg[2]]
		if !ok {
			apiError(w, 404, "ErrorItemNotFound", "The specified object was not found in the store.")
			return true
		}
		switch {
		case len(seg) == 3 && r.Method == http.MethodGet:
			writeJSON(w, 200, g.renderCal(e))
		case len(seg) == 3 && r.Method == http.MethodPatch:
			if !guestPatchOK(e, body) {
				apiError(w, 403, "ErrorAccessDenied", "Only the organizer can change this meeting.")
				return true
			}
			g.applyCal(e, body)
			writeJSON(w, 200, g.renderCal(e))
		case len(seg) == 3 && r.Method == http.MethodDelete:
			delete(g.calEvents, e.ID)
			g.calRemoved = append(g.calRemoved, e.ID)
			w.WriteHeader(http.StatusNoContent)
		case len(seg) == 4 && r.Method == http.MethodPost:
			e.Response = map[string]string{"accept": "accepted", "tentativelyAccept": "tentativelyAccepted", "decline": "declined"}[seg[3]]
			w.WriteHeader(http.StatusAccepted)
		default:
			apiError(w, 400, "BadRequest", "unsupported")
		}
	default:
		return false
	}
	return true
}

func (g *Graph) applyCal(e *CalEvent, b map[string]any) {
	if v, ok := b["subject"].(string); ok {
		e.Title = v
	}
	if v, ok := b["isAllDay"].(bool); ok {
		e.AllDay = v
	}
	parse := func(v any) time.Time {
		m, _ := v.(map[string]any)
		loc := time.UTC
		if tz := fmt.Sprint(m["timeZone"]); tz == "W. Europe Standard Time" {
			loc, _ = time.LoadLocation("Europe/Berlin")
		}
		t, _ := time.ParseInLocation("2006-01-02T15:04:05", fmt.Sprint(m["dateTime"]), loc)
		return t.UTC()
	}
	if v, ok := b["start"]; ok {
		e.Start = parse(v)
	}
	if v, ok := b["end"]; ok {
		e.End = parse(v)
	}
	if on, ok := b["isReminderOn"].(bool); ok {
		e.Reminders = nil
		if n, ok := b["reminderMinutesBeforeStart"].(float64); on && ok {
			e.Reminders = []int{int(n)}
		}
	}
}
