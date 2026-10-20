package ai

import "testing"

func TestParseEventJSON(t *testing.T) {
	d, err := parseEventJSON("Sure!\n```json\n{\"title\":\" Lunch with Sam \",\"date\":\"2026-10-23\",\"startTime\":\"12:30\",\"endDate\":\"\",\"endTime\":\"13:30\",\"allDay\":false,\"location\":\"Coffee Co\",\"attendees\":[\"Sam\",\" \"],\"videoCall\":false,\"repeat\":\"sometimes\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Lunch with Sam" || d.Date != "2026-10-23" || d.StartTime != "12:30" || d.EndTime != "13:30" || d.Location != "Coffee Co" {
		t.Fatalf("%+v", d)
	}
	if len(d.Attendees) != 1 || d.Repeat != "none" {
		t.Fatalf("attendees %v repeat %q", d.Attendees, d.Repeat)
	}
	d, _ = parseEventJSON(`{"title":"x","date":"friday","startTime":"25:99","endTime":"10:00"}`)
	if d.Date != "" || d.StartTime != "" || d.EndTime != "" {
		t.Fatalf("invalid values kept: %+v", d)
	}
	if _, err := parseEventJSON("no idea"); err == nil {
		t.Fatal("expected an error")
	}
}
