package gcal

import (
	"context"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/models"
)

// Patching attendees replaces Google's whole list, and a missing
// responseStatus means "needsAction": guests' known answers must be sent
// along, or editing the guest list wipes everyone's RSVP.
func TestUpdateKeepsAttendeeResponses(t *testing.T) {
	c, f := newTest(t)
	me := calendar.Calendar{RemoteID: "me@gmail.com", CanEdit: true, Primary: true}
	ev := calendar.Event{Title: "Sync", Start: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		Attendees: []models.Attendee{{Email: "me@gmail.com", Response: models.ResponseOrganizer},
			{Email: "a@x.com", Response: models.ResponseAccepted}, {Email: "b@x.com", Response: models.ResponseDeclined},
			{Email: "new@x.com"}}}
	if _, err := c.Update(context.Background(), me, "single", ev, models.ScopeThis); err != nil {
		t.Fatal(err)
	}
	atts := f.bodies[len(f.bodies)-1]["attendees"].([]any)
	got := map[string]any{}
	for _, a := range atts {
		m := a.(map[string]any)
		got[m["email"].(string)] = m["responseStatus"]
	}
	if got["a@x.com"] != "accepted" || got["b@x.com"] != "declined" || got["me@gmail.com"] != "accepted" || got["new@x.com"] != nil {
		t.Fatalf("attendees sent %v", got)
	}
}
