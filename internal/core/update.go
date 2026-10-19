package core

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/update"
)

const updateStateKey = "updateCheck"

type updateState struct {
	LastCheck time.Time `json:"lastCheck"`
	Notified  string    `json:"notified"` // latest version already announced
}

// Update check cadence.
var (
	updateFirstDelay = time.Minute
	updateEvery      = 24 * time.Hour
	updateHTTPClient = &http.Client{Timeout: 20 * time.Second}
)

// CheckForUpdate asks GitHub for the latest release now.
func (s *Service) CheckForUpdate() (models.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	info, err := update.Check(ctx, s.opts.Version, updateHTTPClient)
	if err == nil {
		var st updateState
		_ = s.store.GetSetting(updateStateKey, &st)
		st.LastCheck = time.Now()
		_ = s.store.SetSetting(updateStateKey, st)
	}
	return info, err
}

// updateLoop checks once a day (when enabled) and announces a new version
// once via update:available.
func (s *Service) updateLoop() {
	select {
	case <-time.After(updateFirstDelay):
	case <-s.ctx.Done():
		return
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		var st updateState
		_ = s.store.GetSetting(updateStateKey, &st)
		if s.Settings().CheckUpdates && time.Since(st.LastCheck) >= updateEvery {
			info, err := s.CheckForUpdate()
			if err != nil {
				slog.Debug("update check failed", "err", err)
			} else if info.Available {
				_ = s.store.GetSetting(updateStateKey, &st)
				if st.Notified != info.Latest {
					st.Notified = info.Latest
					_ = s.store.SetSetting(updateStateKey, st)
					slog.Info("update available", "current", info.Current, "latest", info.Latest)
					s.emit(models.EventUpdateAvailable, info)
				}
			}
		}
		select {
		case <-tick.C:
		case <-s.ctx.Done():
			return
		}
	}
}
