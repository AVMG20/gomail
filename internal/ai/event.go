package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

const eventPrompt = `You turn a short description of a calendar event into JSON for a calendar app.
Now is %s (time zone %s). Resolve relative dates ("tomorrow", "next friday", "in two weeks") from now; a weekday without "next" means the next one on or after today.
Reply with one JSON object only, no code fences, with these keys:
{"title": string, "date": "YYYY-MM-DD", "startTime": "HH:MM" (24-hour, "" if no time), "endDate": "YYYY-MM-DD" or "", "endTime": "HH:MM" or "", "allDay": bool,
 "location": string, "description": string, "attendees": [string], "videoCall": bool, "repeat": "none"|"daily"|"weekly"|"monthly"|"yearly"}
Rules:
- title: short and natural, in the language of the description, without the date, time, place or people's names when those are captured elsewhere (keep a name if it is the point, e.g. "Lunch with Sam").
- allDay is true only when no time of day is given (birthdays, holidays, "all day").
- endTime from a duration ("for an hour") or an end ("until 3"); leave "" when unknown.
- attendees: email addresses and names of people to invite; not the user.
- videoCall: true when a video call, Teams, Meet or online meeting is asked for.
- Use "" or false for anything not mentioned. Never invent details.`

// ParseEvent turns natural language ("lunch with Sam friday 12:30 at
// Coffee Co") into event fields. now (with its location) anchors relative dates.
func (c *Client) ParseEvent(ctx context.Context, model, text string, now time.Time) (models.EventDraft, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return models.EventDraft{}, errors.New("describe the event first")
	}
	if len(text) > 2000 {
		return models.EventDraft{}, errors.New("that description is too long; keep it to a sentence or two")
	}
	system := fmt.Sprintf(eventPrompt, now.Format("Monday 2006-01-02 15:04"), now.Location().String())
	out, err := c.Chat(ctx, model, system, text, 0)
	if err != nil {
		return models.EventDraft{}, err
	}
	return parseEventJSON(out)
}

// parseEventJSON reads the model's reply, tolerating code fences or chatter
// around the object, and drops values that don't have the expected shape.
func parseEventJSON(out string) (models.EventDraft, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		return models.EventDraft{}, errors.New("the AI model didn't return event details; try rephrasing")
	}
	var d models.EventDraft
	if err := json.Unmarshal([]byte(out[i:j+1]), &d); err != nil {
		return models.EventDraft{}, errors.New("the AI model returned something unreadable; try again")
	}
	d.Title, d.Location, d.Description = strings.TrimSpace(d.Title), strings.TrimSpace(d.Location), strings.TrimSpace(d.Description)
	valid := func(v, layout string) string {
		v = strings.TrimSpace(v)
		if _, err := time.Parse(layout, v); err != nil {
			return ""
		}
		return v
	}
	d.Date, d.EndDate = valid(d.Date, "2006-01-02"), valid(d.EndDate, "2006-01-02")
	d.StartTime, d.EndTime = valid(d.StartTime, "15:04"), valid(d.EndTime, "15:04")
	if d.Date == "" {
		d.EndDate = ""
	}
	if d.StartTime == "" {
		d.EndTime = ""
	}
	switch d.Repeat {
	case "daily", "weekly", "monthly", "yearly":
	default:
		d.Repeat = "none"
	}
	var people []string
	for _, a := range d.Attendees {
		if a = strings.TrimSpace(a); a != "" && len(people) < 50 {
			people = append(people, a)
		}
	}
	d.Attendees = people
	return d, nil
}
