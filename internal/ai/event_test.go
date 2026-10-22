package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

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

func TestParseEventRequest(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"content": `{"title":"Vet","date":"2026-10-22","startTime":"09:00","allDay":false}`}}}})
	}, "sk-test")
	now := time.Date(2026, 10, 21, 19, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	d, err := c.ParseEvent(context.Background(), "openai/gpt-5", "dierenarts morgen 9 uur", now, "low")
	if err != nil || d.Date != "2026-10-22" || d.StartTime != "09:00" {
		t.Fatalf("draft %+v %v", d, err)
	}
	// The effort is passed on; the prompt carries today's date and time.
	if r, _ := got["reasoning"].(map[string]any); r["effort"] != "low" {
		t.Errorf("reasoning %v", got["reasoning"])
	}
	msgs, _ := got["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)
	if s, _ := sys["content"].(string); !strings.Contains(s, "Wednesday 2026-10-21 19:30") {
		t.Errorf("system prompt lacks now: %q", s)
	}
}
