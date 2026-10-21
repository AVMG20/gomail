package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device sync (internal/devsync) replaces a message's label decisions and
// an account's calendar choices with another device's, instead of merging
// them in like a restored backup.

// DeleteSettingsPrefix removes the stored settings whose key starts with
// prefix.
func (s *Store) DeleteSettingsPrefix(prefix string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE substr(key, 1, length(?1)) = ?1`, prefix)
	return err
}

// SetLabelMarks replaces the label decisions of an account's messages
// (remote message id -> marks; an empty list clears them): manual and AI
// labels, rejections and overrides are set to exactly the marks. Messages
// that haven't synced yet get them when they arrive. A message stays
// AI-labeled once it was (automatic labeling skips it).
func (s *Store) SetLabelMarks(accountID string, marks map[string][]LabelMark) error {
	provider, email, err := accountKey(s.db, accountID)
	if err != nil {
		return err
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for remoteID, list := range marks {
			if remoteID == "" {
				continue
			}
			var msgID string
			err := tx.QueryRow(`SELECT id FROM messages WHERE account_id = ? AND remote_id = ?`, accountID, remoteID).Scan(&msgID)
			switch {
			case err == nil:
				for _, q := range []string{
					`DELETE FROM message_labels WHERE message_id = ? AND source IN ('manual', 'ai')`,
					`DELETE FROM label_rejections WHERE message_id = ?`,
					`DELETE FROM label_overrides WHERE message_id = ?`,
				} {
					if _, err := tx.Exec(q, msgID); err != nil {
						return err
					}
				}
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
			if _, err := tx.Exec(`DELETE FROM pending_labels WHERE provider = ? AND email = ? AND remote_id = ?`, provider, email, remoteID); err != nil {
				return err
			}
			for _, m := range list {
				if !validMark(m.Kind) || (m.Kind == MarkClassified) != (m.LabelID == "") {
					continue
				}
				conf := 1.0
				if m.Kind == MarkAI && m.Confidence > 0 && m.Confidence <= 1 {
					conf = m.Confidence
				}
				if _, err := tx.Exec(`INSERT OR REPLACE INTO pending_labels(provider, email, remote_id, label_id, kind, confidence) VALUES(?,?,?,?,?,?)`,
					provider, email, remoteID, m.LabelID, m.Kind, conf); err != nil {
					return err
				}
			}
		}
		_, err := applyPendingLabels(tx, accountID, time.Now().UnixMilli())
		return err
	})
}

// SetCalendarPrefs replaces an account's calendar choices: calendars not
// in prefs are shown in the provider's color; prefs for calendars that
// haven't synced yet apply when they arrive.
func (s *Store) SetCalendarPrefs(accountID string, prefs []CalendarPref) error {
	provider, email, err := accountKey(s.db, accountID)
	if err != nil {
		return err
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE calendars SET visible = 1, color_override = '' WHERE account_id = ?`, accountID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM pending_calendars WHERE provider = ? AND email = ?`, provider, email); err != nil {
			return err
		}
		for _, p := range prefs {
			if p.RemoteID == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO pending_calendars(provider, email, remote_id, visible, color) VALUES(?,?,?,?,?)`,
				provider, email, p.RemoteID, boolInt(p.Visible), p.Color); err != nil {
				return err
			}
		}
		return applyPendingCalendars(tx, accountID)
	})
}
