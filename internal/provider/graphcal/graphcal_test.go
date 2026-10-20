package graphcal

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
	url       string
	denied    bool
	stateGone bool
	requests  []string
	bodies    []map[string]any
	badPrefer int
}

func ev(id, subject, start, end string, extra map[string]any) map[string]any {
	m := map[string]any{"id": id, "subject": subject, "start": map[string]any{"dateTime": start, "timeZone": "UTC"},
		"end": map[string]any{"dateTime": end, "timeZone": "UTC"}, "type": "singleInstance", "isOrganizer": true,
		"originalStartTimeZone": "W. Europe Standard Time", "body": map[string]any{"contentType": "text", "content": "Agenda"},
		"lastModifiedDateTime": "2026-10-01T10:00:00Z"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (f *fake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if p := r.Header.Get("Prefer"); !strings.Contains(p, `IdType="ImmutableId"`) || !strings.Contains(p, `outlook.timezone="UTC"`) {
			f.badPrefer++
		}
		var body map[string]any
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			json.Unmarshal(data, &body)
			f.bodies = append(f.bodies, body)
		}
		denied, gone := f.denied, f.stateGone
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		if denied {
			w.WriteHeader(403)
			write(map[string]any{"error": map[string]any{"code": "ErrorAccessDenied", "message": "Access is denied. Check credentials and try again."}})
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/me/calendars":
			write(map[string]any{"value": []any{
				map[string]any{"id": "cal-main", "name": "Calendar", "color": "lightBlue", "hexColor": "", "isDefaultCalendar": true, "canEdit": true},
				map[string]any{"id": "cal-team", "name": "Team", "hexColor": "#c239b3", "canEdit": false},
			}})
		case r.URL.Path == "/me/calendarView/delta":
			if q.Get("$deltatoken") != "" {
				if gone {
					w.WriteHeader(410)
					write(map[string]any{"error": map[string]any{"code": "SyncStateNotFound", "message": "gone"}})
					return
				}
				write(map[string]any{"value": []any{
					map[string]any{"id": "e1", "@removed": map[string]any{"reason": "deleted"}},
					ev("e9", "Later", "2026-10-22T08:00:00.0000000", "2026-10-22T09:00:00.0000000", nil),
				}, "@odata.deltaLink": f.url + "/me/calendarView/delta?$deltatoken=d2"})
				return
			}
			if q.Get("$skiptoken") == "" {
				if q.Get("startDateTime") == "" || q.Get("endDateTime") == "" {
					t.Errorf("delta without range: %v", q)
				}
				write(map[string]any{"value": []any{
					ev("e1", "Planning", "2026-10-20T08:00:00.0000000", "2026-10-20T09:00:00.0000000", map[string]any{
						"onlineMeeting": map[string]any{"joinUrl": "https://teams.microsoft.com/l/x"}, "iCalUId": "uid-1",
						"location": map[string]any{"displayName": "Room 1"}, "isReminderOn": true, "reminderMinutesBeforeStart": 15,
						"attendees": []any{map[string]any{"emailAddress": map[string]any{"name": "Ann", "address": "ann@x.com"},
							"type": "required", "status": map[string]any{"response": "tentativelyAccepted"}}}}),
					ev("e2", "Holiday", "2026-10-24T00:00:00.0000000", "2026-10-26T00:00:00.0000000", map[string]any{"isAllDay": true}),
				}, "@odata.nextLink": f.url + "/me/calendarView/delta?$skiptoken=s1"})
				return
			}
			write(map[string]any{"value": []any{
				ev("occ1", "Standup", "2026-10-19T07:00:00.0000000", "2026-10-19T07:15:00.0000000", map[string]any{
					"type": "occurrence", "seriesMasterId": "master1", "isOrganizer": false, "responseStatus": map[string]any{"response": "accepted"},
					"organizer": map[string]any{"emailAddress": map[string]any{"name": "Boss", "address": "boss@x.com"}}}),
				ev("cx", "Cancelled", "2026-10-21T07:00:00.0000000", "2026-10-21T08:00:00.0000000", map[string]any{"isCancelled": true}),
			}, "@odata.deltaLink": f.url + "/me/calendarView/delta?$deltatoken=d1"})
		case r.URL.Path == "/me/events/master1" && r.Method == "GET":
			write(map[string]any{"id": "master1", "type": "seriesMaster", "start": map[string]any{"dateTime": "2026-01-05T08:00:00.0000000", "timeZone": "UTC"},
				"end": map[string]any{"dateTime": "2026-01-05T08:15:00.0000000", "timeZone": "UTC"}, "isOrganizer": true,
				"recurrence": map[string]any{"pattern": map[string]any{"type": "weekly", "interval": 1, "daysOfWeek": []string{"monday", "wednesday"}},
					"range": map[string]any{"type": "noEnd", "startDate": "2026-01-05"}}})
		case r.URL.Path == "/me/calendars/cal-team/calendarView":
			write(map[string]any{"value": []any{ev("t1", "Team lunch", "2026-10-23T11:00:00.0000000", "2026-10-23T12:00:00.0000000", nil)}})
		case r.URL.Path == "/me/calendars/cal-main/events" && r.Method == "POST":
			write(ev("new1", body["subject"].(string), "2026-11-02T09:00:00.0000000", "2026-11-02T10:00:00.0000000", nil))
		case strings.HasPrefix(r.URL.Path, "/me/events/") && r.Method == "PATCH":
			write(ev(strings.TrimPrefix(r.URL.Path, "/me/events/"), "Patched", "2026-01-05T08:30:00.0000000", "2026-01-05T08:45:00.0000000", nil))
		case strings.HasPrefix(r.URL.Path, "/me/events/") && r.Method == "DELETE":
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/tentativelyAccept") && r.Method == "POST":
			w.WriteHeader(202)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}
}

func newTest(t *testing.T) (*Client, *fake) {
	f := &fake{}
	srv := httptest.NewServer(f.handler(t))
	f.url = srv.URL
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c, f
}

func TestCalendarsDeltaAndViews(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	cals, err := c.Calendars(ctx)
	if err != nil || len(cals) != 2 || !cals[0].Primary || cals[0].Color != "#8ab4f8" || cals[1].CanEdit || cals[1].Color != "#c239b3" {
		t.Fatalf("%+v %v", cals, err)
	}
	start, end := calendar.WindowFor(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	res, err := c.Sync(ctx, cals[0], calendar.SyncRequest{Start: start, End: end})
	if err != nil || !res.Full || !strings.Contains(res.State, "$deltatoken=d1") || len(res.Upserts) != 3 || len(res.Deleted) != 1 {
		t.Fatalf("delta %+v %v", res, err)
	}
	plan, hol, occ := res.Upserts[0], res.Upserts[1], res.Upserts[2]
	if !plan.Start.Equal(time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)) || plan.TimeZone != "Europe/Berlin" || plan.Location != "Room 1" ||
		plan.MeetingURL != "https://teams.microsoft.com/l/x" || plan.Description != "Agenda" || plan.ICalUID != "uid-1" ||
		len(plan.Reminders) != 1 || plan.Reminders[0] != 15 || plan.Attendees[0].Response != models.ResponseTentative ||
		plan.MyResponse != models.ResponseOrganizer || !plan.CanEdit {
		t.Fatalf("planning %+v", plan)
	}
	if !hol.AllDay || !hol.Start.Equal(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)) || hol.End.Sub(hol.Start) != 48*time.Hour {
		t.Fatalf("all-day %+v", hol)
	}
	if !occ.IsRecurring || occ.SeriesRemoteID != "master1" || occ.Recurrence != "Weekly on Monday and Wednesday" ||
		occ.MyResponse != models.ResponseAccepted || occ.CanEdit || occ.Organizer.Email != "boss@x.com" {
		t.Fatalf("occurrence %+v", occ)
	}
	if res.Deleted[0] != "cx" {
		t.Fatalf("cancelled %v", res.Deleted)
	}
	// Incremental round with a removal.
	res, err = c.Sync(ctx, cals[0], calendar.SyncRequest{Start: start, End: end, State: res.State})
	if err != nil || res.Full || len(res.Upserts) != 1 || len(res.Deleted) != 1 || res.Deleted[0] != "e1" || !strings.Contains(res.State, "d2") {
		t.Fatalf("incremental %+v %v", res, err)
	}
	// Expired delta: full round.
	f.mu.Lock()
	f.stateGone = true
	f.mu.Unlock()
	res, err = c.Sync(ctx, cals[0], calendar.SyncRequest{Start: start, End: end, State: f.url + "/me/calendarView/delta?$deltatoken=d1"})
	if err != nil || !res.Full || len(res.Upserts) != 3 {
		t.Fatalf("after 410 %+v %v", res, err)
	}
	// Other calendars: full calendarView fetch.
	res, err = c.Sync(ctx, cals[1], calendar.SyncRequest{Start: start, End: end})
	if err != nil || !res.Full || res.State != "" || len(res.Upserts) != 1 || res.Upserts[0].Title != "Team lunch" {
		t.Fatalf("team %+v %v", res, err)
	}
	if f.badPrefer != 0 {
		t.Fatalf("%d requests without the UTC/ImmutableId Prefer header", f.badPrefer)
	}
}

func TestCRUDRespondAndScopes(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	main := calendar.Calendar{RemoteID: "cal-main", Primary: true, CanEdit: true}
	in := models.EventInput{Title: "Review", Start: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), TimeZone: "Europe/Amsterdam",
		Attendees: []models.Attendee{{Email: "a@x.com", Optional: true}}, ReminderMinutes: []int{5},
		Recurrence: models.EventRecurrence{Frequency: models.RepeatMonthly, Count: 6}}
	if err := calendar.NormalizeInput(&in); err != nil {
		t.Fatal(err)
	}
	created, err := c.Create(ctx, main, calendar.FromInput(in))
	if err != nil || created.RemoteID != "new1" {
		t.Fatalf("create %+v %v", created, err)
	}
	b := f.bodies[len(f.bodies)-1]
	st := b["start"].(map[string]any)
	if st["timeZone"] != "W. Europe Standard Time" || st["dateTime"] != "2026-11-02T10:00:00" {
		t.Fatalf("start %v", st)
	}
	rec := b["recurrence"].(map[string]any)
	pat, rng := rec["pattern"].(map[string]any), rec["range"].(map[string]any)
	if pat["type"] != "absoluteMonthly" || pat["dayOfMonth"] != float64(2) || rng["type"] != "numbered" || rng["numberOfOccurrences"] != float64(6) {
		t.Fatalf("recurrence %v", rec)
	}
	if b["reminderMinutesBeforeStart"] != float64(5) || b["attendees"].([]any)[0].(map[string]any)["type"] != "optional" {
		t.Fatalf("body %v", b)
	}
	// Series update: PATCH the master shifted by the occurrence change.
	upd := calendar.Event{Title: "Standup", SeriesRemoteID: "master1", OriginalStart: time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC),
		Start: time.Date(2026, 10, 19, 7, 30, 0, 0, time.UTC), End: time.Date(2026, 10, 19, 7, 45, 0, 0, time.UTC)}
	if _, err := c.Update(ctx, main, "occ1", upd, models.ScopeSeries); err != nil {
		t.Fatal(err)
	}
	if last := f.requests[len(f.requests)-1]; !strings.HasPrefix(last, "PATCH /me/events/master1") {
		t.Fatalf("series target %s", last)
	}
	if s := f.bodies[len(f.bodies)-1]["start"].(map[string]any); s["dateTime"] != "2026-01-05T08:30:00" || s["timeZone"] != "UTC" {
		t.Fatalf("shifted %v", s)
	}
	if _, ok := f.bodies[len(f.bodies)-1]["recurrence"]; ok {
		t.Fatal("series rule rewritten without a change")
	}
	// An occurrence alone.
	if _, err := c.Update(ctx, main, "occ1", upd, models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	if last := f.requests[len(f.requests)-1]; !strings.HasPrefix(last, "PATCH /me/events/occ1") {
		t.Fatalf("occurrence target %s", last)
	}
	if err := c.Delete(ctx, main, "e1", models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	if err := c.Respond(ctx, main, "occ1", models.ResponseTentative); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(ctx, calendar.Calendar{RemoteID: "cal-team"}, calendar.FromInput(in)); !errors.Is(err, calendar.ErrReadOnly) {
		t.Fatalf("read-only %v", err)
	}
	f.mu.Lock()
	f.denied = true
	f.mu.Unlock()
	if _, err := c.Calendars(ctx); !errors.Is(err, calendar.ErrNoConsent) {
		t.Fatalf("denied %v", err)
	}
}
