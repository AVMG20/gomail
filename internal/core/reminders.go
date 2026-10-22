package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// Event reminders: every reminderEvery the events of the next week are
// checked; a reminder fires once when its time has come (up to
// reminderGrace late, e.g. after sleep or a restart). Fired reminders are
// remembered in the settings table, so restarts don't repeat them.
var (
	reminderEvery = 30 * time.Second
	reminderGrace = 10 * time.Minute
)

const (
	reminderHorizon = 7 * 24 * time.Hour
	remindedKey     = "calendarReminded"
)

func (s *Service) reminderLoop() {
	tick := time.NewTicker(reminderEvery)
	defer tick.Stop()
	for {
		s.checkReminders()
		select {
		case <-tick.C:
		case <-s.ctx.Done():
			return
		}
	}
}

// checkReminders shows the reminders that are due now.
func (s *Service) checkReminders() {
	st := s.Settings()
	if !st.CalendarNotifications {
		return
	}
	now := nowFunc()
	evs, err := s.store.ReminderEvents(now.Add(-reminderGrace), now.Add(reminderHorizon))
	if err != nil {
		return
	}
	reminded := map[string]int64{}
	_ = s.store.GetSetting(remindedKey, &reminded)
	changed := false
	for _, ev := range evs {
		if ev.Status == models.EventCancelled || ev.MyResponse == models.ResponseDeclined {
			continue
		}
		mins := ev.Reminders
		if len(mins) == 0 {
			if ev.RemindersOff {
				continue
			}
			mins = []int{st.CalendarDefaultReminder}
		}
		start := ev.Start
		if ev.AllDay { // all-day events start at local midnight
			start = time.Date(ev.Start.Year(), ev.Start.Month(), ev.Start.Day(), 0, 0, 0, 0, time.Local)
		}
		for _, m := range mins {
			at := start.Add(-time.Duration(m) * time.Minute)
			if at.After(now) || now.Sub(at) > reminderGrace {
				continue
			}
			key := ev.ID + "@" + strconv.FormatInt(ev.Start.Unix(), 10) + "-" + strconv.Itoa(m)
			if _, done := reminded[key]; done {
				continue
			}
			reminded[key] = now.Unix()
			changed = true
			title, body := ReminderText(ev, now, st.CalendarTimeFormat)
			s.ui.Notify(title, body, models.NotificationOpen{AccountID: ev.AccountID, EventID: ev.ID})
		}
	}
	// Forget reminders older than two days.
	for k, t := range reminded {
		if now.Sub(time.Unix(t, 0)) > 48*time.Hour {
			delete(reminded, k)
			changed = true
		}
	}
	if changed {
		_ = s.store.SetSetting(remindedKey, reminded)
	}
}

// ReminderText renders a reminder: the title and "In 10 minutes · 14:00 ·
// Room 1" (times in the local zone; "2:00 PM" with timeFormat "12h").
func ReminderText(ev models.CalendarEvent, now time.Time, timeFormat string) (string, string) {
	clock := "15:04"
	if timeFormat == "12h" {
		clock = "3:04 PM"
	}
	title := ev.Title
	if strings.TrimSpace(title) == "" {
		title = "(no title)"
	}
	var parts []string
	until := ev.Start.Sub(now).Round(time.Minute)
	switch {
	case ev.AllDay:
		parts = append(parts, "All day "+ev.Start.Format("Mon Jan 2"))
	case until <= 0:
		parts = append(parts, "Now")
	case until < time.Hour:
		n := int(until.Minutes())
		unit := "minutes"
		if n == 1 {
			unit = "minute"
		}
		parts = append(parts, fmt.Sprintf("In %d %s", n, unit))
	default:
		parts = append(parts, "At "+ev.Start.In(time.Local).Format("Mon "+clock))
	}
	if !ev.AllDay && until > 0 && until < time.Hour {
		parts = append(parts, ev.Start.In(time.Local).Format(clock))
	}
	if ev.Location != "" {
		parts = append(parts, ev.Location)
	}
	return title, strings.Join(parts, " · ")
}
