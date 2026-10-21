package gcal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
)

type fake struct {
	mu        sync.Mutex
	noScope   bool
	tokenGone bool
	requests  []string
	bodies    []map[string]any
	ifMatch   []string
}

func (f *fake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		var body map[string]any
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			json.Unmarshal(data, &body)
			f.bodies = append(f.bodies, body)
		}
		noScope, gone := f.noScope, f.tokenGone
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		if noScope {
			w.WriteHeader(403)
			write(map[string]any{"error": map[string]any{"code": 403, "message": "Request had insufficient authentication scopes.",
				"status": "PERMISSION_DENIED", "errors": []any{map[string]any{"reason": "insufficientPermissions"}}}})
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/users/me/calendarList":
			write(map[string]any{"items": []any{
				map[string]any{"id": "me@gmail.com", "summary": "Me", "backgroundColor": "#9fe1e7", "primary": true, "accessRole": "owner",
					"timeZone": "Europe/Amsterdam", "defaultReminders": []any{map[string]any{"method": "popup", "minutes": 30}},
					"conferenceProperties": map[string]any{"allowedConferenceSolutionTypes": []string{"eventHangout", "hangoutsMeet"}}},
				map[string]any{"id": "holidays", "summary": "Holidays", "summaryOverride": "Dutch holidays", "accessRole": "reader"},
				map[string]any{"id": "gone", "summary": "Gone", "deleted": true},
			}})
		case r.URL.Path == "/calendars/me@gmail.com/events" && r.Method == "GET":
			if tok := q.Get("syncToken"); tok != "" {
				if gone || tok != "tok1" {
					w.WriteHeader(410)
					write(map[string]any{"error": map[string]any{"code": 410, "message": "Sync token is no longer valid"}})
					return
				}
				if q.Get("timeMin") != "" {
					t.Error("timeMin sent with a sync token")
				}
				write(map[string]any{"items": []any{
					map[string]any{"id": "single", "status": "cancelled"},
					map[string]any{"id": "new", "status": "confirmed", "summary": "New", "start": map[string]any{"dateTime": "2026-10-21T10:00:00+02:00"},
						"end": map[string]any{"dateTime": "2026-10-21T11:00:00+02:00"}},
				}, "nextSyncToken": "tok2"})
				return
			}
			if q.Get("singleEvents") != "true" || q.Get("timeMin") == "" || q.Get("timeMax") == "" {
				t.Errorf("full sync query %v", q)
			}
			if q.Get("pageToken") == "" {
				write(map[string]any{"items": []any{
					map[string]any{"id": "single", "status": "confirmed", "summary": "Lunch", "description": "<b>Bring</b> the slides",
						"location": "Café", "start": map[string]any{"dateTime": "2026-10-20T12:00:00+02:00", "timeZone": "Europe/Amsterdam"},
						"end":     map[string]any{"dateTime": "2026-10-20T13:00:00+02:00", "timeZone": "Europe/Amsterdam"},
						"iCalUID": "single@google.com", "hangoutLink": "https://meet.google.com/abc", "reminders": map[string]any{"useDefault": true},
						"organizer": map[string]any{"email": "boss@x.com", "displayName": "Boss"},
						"attendees": []any{map[string]any{"email": "boss@x.com", "organizer": true, "responseStatus": "accepted"},
							map[string]any{"email": "me@gmail.com", "self": true, "responseStatus": "needsAction"}}},
					map[string]any{"id": "allday", "summary": "Holiday", "start": map[string]any{"date": "2026-10-24"}, "end": map[string]any{"date": "2026-10-26"}},
				}, "nextPageToken": "p2"})
				return
			}
			write(map[string]any{"items": []any{
				map[string]any{"id": "weekly_20261019T070000Z", "recurringEventId": "weekly", "summary": "Standup",
					"start": map[string]any{"dateTime": "2026-10-19T09:00:00+02:00"}, "end": map[string]any{"dateTime": "2026-10-19T09:15:00+02:00"},
					"organizer":      map[string]any{"email": "me@gmail.com", "self": true},
					"conferenceData": map[string]any{"entryPoints": []any{map[string]any{"entryPointType": "video", "uri": "https://meet.google.com/xyz"}}}},
			}, "nextSyncToken": "tok1"})
		case r.URL.Path == "/calendars/me@gmail.com/events/weekly" && r.Method == "GET":
			write(map[string]any{"id": "weekly", "recurrence": []string{"RRULE:FREQ=WEEKLY;BYDAY=MO"}, "summary": "Standup",
				"start": map[string]any{"dateTime": "2026-01-05T09:00:00+01:00"}, "end": map[string]any{"dateTime": "2026-01-05T09:15:00+01:00"},
				"organizer": map[string]any{"email": "me@gmail.com", "self": true}})
		case r.URL.Path == "/calendars/me@gmail.com/events/single" && r.Method == "GET":
			write(map[string]any{"id": "single", "organizer": map[string]any{"email": "boss@x.com"},
				"attendees": []any{map[string]any{"email": "boss@x.com", "organizer": true},
					map[string]any{"email": "me@gmail.com", "self": true, "responseStatus": "needsAction"}}})
		case r.URL.Path == "/calendars/me@gmail.com/events/x" && r.Method == "GET":
			write(map[string]any{"id": "x", "organizer": map[string]any{"email": "me@gmail.com", "self": true}})
		case r.URL.Path == "/calendars/me@gmail.com/events/gone" && r.Method == "GET":
			w.WriteHeader(410)
		case r.URL.Path == "/calendars/me@gmail.com/events" && r.Method == "POST":
			if q.Get("sendUpdates") != "all" || q.Get("conferenceDataVersion") != "1" {
				t.Errorf("insert query %v", q)
			}
			body["id"], body["organizer"] = "created", map[string]any{"email": "me@gmail.com", "self": true}
			write(body)
		case strings.HasPrefix(r.URL.Path, "/calendars/") && r.Method == "PATCH":
			f.mu.Lock()
			f.ifMatch = append(f.ifMatch, r.Header.Get("If-Match"))
			f.mu.Unlock()
			body["id"] = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if body["start"] == nil {
				body["start"] = map[string]any{"dateTime": "2026-10-20T12:00:00+02:00"}
				body["end"] = map[string]any{"dateTime": "2026-10-20T13:00:00+02:00"}
			}
			write(body)
		case strings.HasSuffix(r.URL.Path, "/move") && r.Method == "POST":
			if q.Get("destination") != "work@group" {
				t.Errorf("move destination %v", q)
			}
			write(map[string]any{"id": "single", "summary": "Lunch"})
		case r.Method == "DELETE":
			// Cancelling your own event tells the guests; removing an invitation tells nobody.
			if want := map[bool]string{true: "none", false: "all"}[strings.HasSuffix(r.URL.Path, "/single")]; q.Get("sendUpdates") != want {
				t.Errorf("delete %s sendUpdates=%q, want %q", r.URL.Path, q.Get("sendUpdates"), want)
			}
			if r.Header.Get("If-Match") == `"stale"` {
				w.WriteHeader(412)
				write(map[string]any{"error": map[string]any{"code": 412, "message": "Precondition Failed"}})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/gone") {
				w.WriteHeader(410)
				return
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}
}

func newTest(t *testing.T) (*Client, *fake) {
	f := &fake{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c, f
}

func TestCalendarsAndSync(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	cals, err := c.Calendars(ctx)
	if err != nil || len(cals) != 2 {
		t.Fatalf("%+v %v", cals, err)
	}
	me, hol := cals[0], cals[1]
	if !me.Primary || !me.CanEdit || me.TimeZone != "Europe/Amsterdam" || me.Color != "#9fe1e7" || len(me.DefaultReminders) != 1 {
		t.Fatalf("primary %+v", me)
	}
	if hol.CanEdit || hol.Name != "Dutch holidays" {
		t.Fatalf("holidays %+v", hol)
	}
	start, end := calendar.WindowFor(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	res, err := c.Sync(ctx, me, calendar.SyncRequest{Start: start, End: end})
	if err != nil || !res.Full || res.State != "tok1" || len(res.Upserts) != 3 {
		t.Fatalf("full sync %+v %v", res, err)
	}
	lunch, allday, standup := res.Upserts[0], res.Upserts[1], res.Upserts[2]
	if !lunch.Start.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) || lunch.TimeZone != "Europe/Amsterdam" ||
		lunch.Description != "Bring the slides" || lunch.MeetingURL != "https://meet.google.com/abc" || lunch.MyResponse != models.ResponseNone ||
		lunch.IsOrganizer || lunch.CanEdit || lunch.Organizer.Email != "boss@x.com" || len(lunch.Reminders) != 1 || lunch.Reminders[0] != 30 ||
		lunch.ICalUID != "single@google.com" || lunch.Attendees[0].Response != models.ResponseOrganizer {
		t.Fatalf("lunch %+v", lunch)
	}
	if !allday.AllDay || !allday.Start.Equal(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)) || allday.End.Sub(allday.Start) != 48*time.Hour {
		t.Fatalf("all-day %+v", allday)
	}
	if !standup.IsRecurring || standup.SeriesRemoteID != "weekly" || standup.Recurrence != "Weekly on Monday" ||
		standup.MeetingURL != "https://meet.google.com/xyz" || !standup.CanEdit || standup.MyResponse != models.ResponseOrganizer {
		t.Fatalf("standup %+v", standup)
	}
	// Incremental.
	res, err = c.Sync(ctx, me, calendar.SyncRequest{Start: start, End: end, State: "tok1"})
	if err != nil || res.Full || res.State != "tok2" || len(res.Deleted) != 1 || res.Deleted[0] != "single" || len(res.Upserts) != 1 {
		t.Fatalf("incremental %+v %v", res, err)
	}
	// 410: full resync.
	f.mu.Lock()
	f.tokenGone = true
	f.mu.Unlock()
	res, err = c.Sync(ctx, me, calendar.SyncRequest{Start: start, End: end, State: "tok1"})
	if err != nil || !res.Full || res.State != "tok1" || len(res.Upserts) != 3 {
		t.Fatalf("after 410 %+v %v", res, err)
	}
	if evs, err := c.Range(ctx, me, start, end); err != nil || len(evs) != 3 {
		t.Fatalf("range %d %v", len(evs), err)
	}
}

func TestCRUDRespondAndScopes(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	me := calendar.Calendar{RemoteID: "me@gmail.com", CanEdit: true, Primary: true}
	in := models.EventInput{Title: "Review", Start: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), TimeZone: "America/New_York",
		Attendees: []models.Attendee{{Email: "a@x.com"}}, ReminderMinutes: []int{15},
		Recurrence: models.EventRecurrence{Frequency: models.RepeatWeekly, Count: 4}}
	if err := calendar.NormalizeInput(&in); err != nil {
		t.Fatal(err)
	}
	ev, err := c.Create(ctx, me, calendar.FromInput(in))
	if err != nil || ev.RemoteID != "created" || !ev.IsOrganizer {
		t.Fatalf("create %+v %v", ev, err)
	}
	b := f.bodies[len(f.bodies)-1]
	start := b["start"].(map[string]any)
	if start["timeZone"] != "America/New_York" || start["dateTime"] != "2026-11-02T04:00:00-05:00" {
		t.Fatalf("start %v", start)
	}
	if rec := b["recurrence"].([]any); len(rec) != 1 || rec[0] != "RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=4" {
		t.Fatalf("recurrence %v", rec)
	}
	if rem := b["reminders"].(map[string]any); rem["useDefault"] != false {
		t.Fatalf("reminders %v", rem)
	}
	// All-day body uses dates.
	allday := calendar.Event{Title: "Off", AllDay: true, Start: time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)}
	if _, err := c.Create(ctx, me, allday); err != nil {
		t.Fatal(err)
	}
	if s := f.bodies[len(f.bodies)-1]["start"].(map[string]any); s["date"] != "2026-12-24" || s["dateTime"] != nil {
		t.Fatalf("all-day start %v", s)
	}
	// Series update targets the master and shifts it by the change.
	upd := calendar.Event{Title: "Standup (later)", SeriesRemoteID: "weekly", OriginalStart: time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC),
		Start: time.Date(2026, 10, 19, 7, 30, 0, 0, time.UTC), End: time.Date(2026, 10, 19, 7, 45, 0, 0, time.UTC)}
	if _, err := c.Update(ctx, me, "weekly_20261019T070000Z", upd, models.ScopeSeries); err != nil {
		t.Fatal(err)
	}
	last := f.requests[len(f.requests)-1]
	if !strings.HasPrefix(last, "PATCH /calendars/me@gmail.com/events/weekly?") {
		t.Fatalf("series patch %s", last)
	}
	ms := f.bodies[len(f.bodies)-1]["start"].(map[string]any)
	if ms["dateTime"] != "2026-01-05T08:30:00Z" { // master 08:00 UTC + 30 min
		t.Fatalf("shifted master start %v", ms)
	}
	if rec := f.bodies[len(f.bodies)-1]["recurrence"].([]any); rec[0] != "RRULE:FREQ=WEEKLY;BYDAY=MO" {
		t.Fatalf("rule lost %v", rec)
	}
	if err := c.Delete(ctx, me, "x", models.ScopeThis, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, me, "gone", models.ScopeThis, ""); err != nil {
		t.Fatalf("already deleted: %v", err)
	}
	if err := c.Respond(ctx, me, "single", models.ResponseTentative); err != nil {
		t.Fatal(err)
	}
	atts := f.bodies[len(f.bodies)-1]["attendees"].([]any)
	if atts[1].(map[string]any)["responseStatus"] != "tentative" {
		t.Fatalf("respond body %v", atts)
	}
	if _, err := c.Create(ctx, calendar.Calendar{RemoteID: "holidays"}, allday); !errors.Is(err, calendar.ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	f.mu.Lock()
	f.noScope = true
	f.mu.Unlock()
	if _, err := c.Calendars(ctx); !errors.Is(err, calendar.ErrNoConsent) {
		t.Fatalf("insufficient scope: %v", err)
	}
}

func TestMoveEtagDSTAndEscaping(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	me := calendar.Calendar{RemoteID: "me@gmail.com", CanEdit: true}
	work := calendar.Calendar{RemoteID: "work@group", CanEdit: true}
	// Crossing the end of DST in Amsterdam (Oct 25 2026, 03:00 → 02:00).
	ev := calendar.Event{Title: "Night shift", TimeZone: "Europe/Amsterdam", Description: "<script>x</script>\nline 2",
		Start: time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC), End: time.Date(2026, 10, 25, 2, 30, 0, 0, time.UTC)}
	if _, err := c.Create(ctx, me, ev); err != nil {
		t.Fatal(err)
	}
	b := f.bodies[len(f.bodies)-1]
	if s, e := b["start"].(map[string]any)["dateTime"], b["end"].(map[string]any)["dateTime"]; s != "2026-10-25T01:30:00+02:00" || e != "2026-10-25T03:30:00+01:00" {
		t.Fatalf("DST times %v %v", s, e)
	}
	if d := b["description"]; d != "&lt;script&gt;x&lt;/script&gt;<br>line 2" {
		t.Fatalf("description %q", d)
	}
	if plainText(b["description"].(string)) != "<script>x</script>\nline 2" {
		t.Fatalf("round trip %q", plainText(b["description"].(string)))
	}
	// Conditional update and delete.
	ev.ETag = `"7"`
	if _, err := c.Update(ctx, me, "single", ev, models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	if got := f.ifMatch[len(f.ifMatch)-1]; got != `"7"` {
		t.Fatalf("If-Match %q", got)
	}
	if err := c.Delete(ctx, me, "single", models.ScopeThis, `"stale"`); !errors.Is(err, calendar.ErrConflict) {
		t.Fatalf("stale delete %v", err)
	}
	// Move: events.move, then the fields are applied in the target.
	moved, err := c.Move(ctx, me, work, "single", calendar.Event{Title: "Lunch (moved)", Start: ev.Start, End: ev.End})
	if err != nil || moved.RemoteID != "single" {
		t.Fatalf("move %+v %v", moved, err)
	}
	if last := f.requests[len(f.requests)-1]; !strings.HasPrefix(last, "PATCH /calendars/work@group/events/single") {
		t.Fatalf("after move %s", last)
	}
	if _, err := c.Move(ctx, me, calendar.Calendar{RemoteID: "ro"}, "single", ev); !errors.Is(err, calendar.ErrReadOnly) {
		t.Fatalf("move to read-only %v", err)
	}
}

func TestVideoCalls(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	cals, _ := c.Calendars(ctx)
	if cals[0].VideoCallProvider != "meet" || cals[1].VideoCallProvider != "" {
		t.Fatalf("providers %q %q", cals[0].VideoCallProvider, cals[1].VideoCallProvider)
	}
	me := cals[0]
	ev := calendar.Event{Title: "Call", AddVideoCall: true, Start: time.Now(), End: time.Now().Add(time.Hour)}
	if _, err := c.Create(ctx, me, ev); err != nil {
		t.Fatal(err)
	}
	cd := f.bodies[len(f.bodies)-1]["conferenceData"].(map[string]any)["createRequest"].(map[string]any)
	if cd["conferenceSolutionKey"].(map[string]any)["type"] != "hangoutsMeet" || len(cd["requestId"].(string)) < 16 {
		t.Fatalf("createRequest %v", cd)
	}
	first := cd["requestId"]
	c.Create(ctx, me, ev)
	if again := f.bodies[len(f.bodies)-1]["conferenceData"].(map[string]any)["createRequest"].(map[string]any)["requestId"]; again == first {
		t.Fatal("requestId reused")
	}
	// A calendar without Meet: no conference request.
	me.VideoCallProvider = ""
	c.Create(ctx, me, ev)
	if _, ok := f.bodies[len(f.bodies)-1]["conferenceData"]; ok {
		t.Fatal("conference requested without Meet")
	}
	// Removing the link detaches the conference (null with conferenceDataVersion=1).
	ev.AddVideoCall, ev.RemoveConference = false, true
	if _, err := c.Update(ctx, cals[0], "e1", ev, models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.bodies[len(f.bodies)-1]["conferenceData"]; !ok || v != nil {
		t.Fatalf("conferenceData %v %v", v, ok)
	}
	// Links in the location are detected when there's no conference data.
	got := c.toEvent(gEvent{ID: "z", Location: "https://us02web.zoom.us/j/123", Start: gTime{DateTime: "2026-10-20T10:00:00Z"}, End: gTime{DateTime: "2026-10-20T11:00:00Z"}}, me)
	if got.MeetingURL != "https://us02web.zoom.us/j/123" {
		t.Fatalf("detected %q", got.MeetingURL)
	}
}

func TestSetRemindersKeepsWhatItDoesntShow(t *testing.T) {
	var patches []map[string]any
	events := map[string]map[string]any{
		// An email reminder the card can't show must survive.
		"mixed": {"id": "mixed", "reminders": map[string]any{"useDefault": false, "overrides": []any{
			map[string]any{"method": "email", "minutes": 1440}, map[string]any{"method": "popup", "minutes": 10}}}},
		"defaults": {"id": "defaults", "reminders": map[string]any{"useDefault": true}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(events[id])
		case "PATCH":
			if r.URL.Query().Get("sendUpdates") != "none" {
				t.Errorf("patch notifies: %v", r.URL.Query())
			}
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			patches = append(patches, b)
			json.NewEncoder(w).Encode(map[string]any{"id": id, "reminders": b["reminders"]})
		}
	}))
	defer srv.Close()
	c := New(httpx.StaticToken("tok"), srv.URL)
	cal := calendar.Calendar{RemoteID: "me@gmail.com", CanEdit: true, DefaultReminders: []int{30}}
	ev, err := c.SetReminders(context.Background(), cal, "mixed", []int{60})
	if err != nil || len(ev.Reminders) != 1 || ev.Reminders[0] != 60 {
		t.Fatalf("mixed: %+v %v", ev.Reminders, err)
	}
	over := patches[0]["reminders"].(map[string]any)["overrides"].([]any)
	if len(over) != 2 || over[0].(map[string]any)["method"] != "email" || len(patches[0]) != 1 {
		t.Fatalf("patch %v", patches[0])
	}
	// Picking the calendar's own default changes nothing.
	if ev, err := c.SetReminders(context.Background(), cal, "defaults", []int{30}); err != nil || len(patches) != 1 || ev.Reminders[0] != 30 {
		t.Fatalf("defaults: %+v %v, %d patches", ev.Reminders, err, len(patches))
	}
}
