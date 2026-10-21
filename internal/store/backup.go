package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Label mark kinds (LabelMark.Kind).
const (
	MarkManual     = "manual"     // label assigned by hand
	MarkAI         = "ai"         // label assigned by the classifier
	MarkRejected   = "rejected"   // label removed by hand: classification doesn't add it back
	MarkOverride   = "override"   // provider/system label removed: sync doesn't add it back
	MarkClassified = "classified" // message was AI-labeled already (LabelID ""): automatic labeling skips it
)

// LabelMark is a portable label decision on one message, keyed by the
// provider's remote message id (local ids differ per install). Backups
// carry them per account; provider and Junk assignments are not included
// because the next sync re-derives them.
type LabelMark struct {
	RemoteID   string  `json:"remoteId"`
	LabelID    string  `json:"labelId,omitempty"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence,omitempty"`
}

// CalendarPref is the user's visibility, color and reminder choice for a
// calendar, keyed by the provider's remote calendar id.
type CalendarPref struct {
	RemoteID string `json:"remoteId"`
	Visible  bool   `json:"visible"`
	Color    string `json:"color,omitempty"` // "" = the provider's color
	Muted    bool   `json:"muted,omitempty"` // no reminders
}

func validMark(kind string) bool {
	switch kind {
	case MarkManual, MarkAI, MarkRejected, MarkOverride, MarkClassified:
		return true
	}
	return false
}

// accountKey returns an account's provider and (lower-cased) email.
func accountKey(q interface {
	QueryRow(string, ...any) *sql.Row
}, accountID string) (provider, email string, err error) {
	err = q.QueryRow(`SELECT provider, email FROM accounts WHERE id = ?`, accountID).Scan(&provider, &email)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return provider, strings.ToLower(email), err
}

// ExportLabelMarks lists the local label decisions of an account's
// messages, including marks restored from a backup that are still waiting
// for their messages to sync.
func (s *Store) ExportLabelMarks(accountID string) ([]LabelMark, error) {
	provider, email, err := accountKey(s.db, accountID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT m.remote_id, ml.label_id, ml.source, ml.confidence FROM message_labels ml JOIN messages m ON m.id = ml.message_id
			WHERE m.account_id = ?1 AND ml.source IN ('manual', 'ai')
		UNION SELECT m.remote_id, r.label_id, 'rejected', 1 FROM label_rejections r JOIN messages m ON m.id = r.message_id WHERE m.account_id = ?1
		UNION SELECT m.remote_id, o.label_id, 'override', 1 FROM label_overrides o JOIN messages m ON m.id = o.message_id WHERE m.account_id = ?1
		UNION SELECT m.remote_id, '', 'classified', 1 FROM messages m WHERE m.account_id = ?1 AND m.classified_at > 0
		UNION SELECT remote_id, label_id, kind, confidence FROM pending_labels WHERE provider = ?2 AND email = ?3
		ORDER BY 1, 3, 2`, accountID, provider, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabelMark{}
	for rows.Next() {
		var m LabelMark
		if err := rows.Scan(&m.RemoteID, &m.LabelID, &m.Kind, &m.Confidence); err != nil {
			return nil, err
		}
		if m.Kind != MarkAI {
			m.Confidence = 0 // implied
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddPendingLabels stores restored label marks of the account provider/email
// until its messages are synced (ApplySync applies them on ingest; call
// ApplyPendingLabels for messages that are already present). Invalid marks
// are skipped; it returns how many were stored.
func (s *Store) AddPendingLabels(provider, email string, marks []LabelMark) (int, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	n := 0
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO pending_labels(provider, email, remote_id, label_id, kind, confidence) VALUES(?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, m := range marks {
			if m.RemoteID == "" || !validMark(m.Kind) || (m.Kind == MarkClassified) != (m.LabelID == "") {
				continue
			}
			conf := 1.0
			if m.Kind == MarkAI && m.Confidence > 0 && m.Confidence <= 1 {
				conf = m.Confidence
			}
			if _, err := st.Exec(provider, email, m.RemoteID, m.LabelID, m.Kind, conf); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// ApplyPendingLabels applies the restored label marks of an account whose
// messages are present and returns how many messages were updated.
func (s *Store) ApplyPendingLabels(accountID string) (int, error) {
	n := 0
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		n, err = applyPendingLabels(tx, accountID, time.Now().UnixMilli())
		return err
	})
	return n, err
}

// PendingLabels counts the restored label marks still waiting for their
// messages ("" provider = all accounts).
func (s *Store) PendingLabels(provider, email string) (int, error) {
	q := `SELECT count(*) FROM pending_labels`
	var args []any
	if provider != "" {
		q += ` WHERE provider = ? AND email = ?`
		args = append(args, provider, strings.ToLower(email))
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// applyPendingLabels moves the pending marks of messages that exist into
// the label tables: overrides drop the provider/system assignment,
// rejections drop the AI assignment, manual assignments win over every
// other source, AI assignments only fill gaps (never re-adding a rejected
// label). It returns the number of messages it touched.
func applyPendingLabels(tx *sql.Tx, accountID string, now int64) (int, error) {
	var provider, email string
	err := tx.QueryRow(`SELECT a.provider, a.email FROM accounts a WHERE a.id = ?
		AND EXISTS (SELECT 1 FROM pending_labels p WHERE p.provider = a.provider AND p.email = a.email)`, accountID).Scan(&provider, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	// ?1 account id, ?2 provider, ?3 email, ?4 kind, ?5 now
	const pend = `FROM pending_labels p JOIN messages m ON m.account_id = ?1 AND m.remote_id = p.remote_id
		WHERE p.provider = ?2 AND p.email = ?3 AND p.kind = ?4`
	steps := []struct {
		kind, q string
	}{
		{MarkOverride, `INSERT OR REPLACE INTO label_overrides(message_id, label_id, created_at) SELECT m.id, p.label_id, ?5 ` + pend},
		{MarkOverride, `DELETE FROM message_labels WHERE source IN ('provider', 'system') AND (message_id, label_id) IN (SELECT m.id, p.label_id ` + pend + `)`},
		{MarkRejected, `INSERT OR REPLACE INTO label_rejections(message_id, label_id, created_at) SELECT m.id, p.label_id, ?5 ` + pend},
		{MarkRejected, `DELETE FROM message_labels WHERE source = 'ai' AND (message_id, label_id) IN (SELECT m.id, p.label_id ` + pend + `)`},
		{MarkManual, `INSERT INTO message_labels(message_id, label_id, source, confidence, created_at) SELECT m.id, p.label_id, 'manual', 1, ?5 ` + pend + `
			ON CONFLICT(message_id, label_id) DO UPDATE SET source = 'manual', confidence = 1`},
		{MarkAI, `INSERT INTO message_labels(message_id, label_id, source, confidence, created_at) SELECT m.id, p.label_id, 'ai', p.confidence, ?5 ` + pend + `
			AND NOT EXISTS (SELECT 1 FROM label_rejections r WHERE r.message_id = m.id AND r.label_id = p.label_id)
			ON CONFLICT(message_id, label_id) DO NOTHING`},
		{MarkClassified, `UPDATE messages SET classified_at = ?5 WHERE account_id = ?1 AND classified_at = 0 AND remote_id IN
			(SELECT p.remote_id FROM pending_labels p WHERE p.provider = ?2 AND p.email = ?3 AND p.kind = ?4)`},
	}
	for _, st := range steps {
		args := []any{accountID, provider, email, st.kind, now}
		if !strings.Contains(st.q, "?5") {
			args = args[:4] // SQLite counts parameters up to the highest index used
		}
		if _, err := tx.Exec(st.q, args...); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`DELETE FROM pending_labels WHERE provider = ? AND email = ? AND remote_id IN
		(SELECT remote_id FROM messages WHERE account_id = ?)`, provider, email, accountID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ExportCalendarPrefs lists the calendars of an account the user hid or
// recolored (plus restored preferences still waiting for their calendar).
func (s *Store) ExportCalendarPrefs(accountID string) ([]CalendarPref, error) {
	provider, email, err := accountKey(s.db, accountID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT remote_id, visible, color_override, muted FROM calendars WHERE account_id = ?1 AND (visible = 0 OR color_override != '' OR muted = 1)
		UNION SELECT remote_id, visible, color, muted FROM pending_calendars WHERE provider = ?2 AND email = ?3
			AND remote_id NOT IN (SELECT remote_id FROM calendars WHERE account_id = ?1)
		ORDER BY 1`, accountID, provider, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CalendarPref{}
	for rows.Next() {
		var p CalendarPref
		var vis, muted int
		if err := rows.Scan(&p.RemoteID, &vis, &p.Color, &muted); err != nil {
			return nil, err
		}
		p.Visible, p.Muted = vis == 1, muted == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPendingCalendars stores restored calendar preferences until the
// calendars sync, and applies them right away to calendars already present.
func (s *Store) AddPendingCalendars(provider, email string, prefs []CalendarPref) (int, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	n := 0
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, p := range prefs {
			if p.RemoteID == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO pending_calendars(provider, email, remote_id, visible, color, muted) VALUES(?,?,?,?,?,?)`,
				provider, email, p.RemoteID, boolInt(p.Visible), p.Color, boolInt(p.Muted)); err != nil {
				return err
			}
			n++
		}
		var id string
		err := tx.QueryRow(`SELECT id FROM accounts WHERE provider = ? AND email = ?`, provider, email).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return applyPendingCalendars(tx, id)
	})
	return n, err
}

// applyPendingCalendars applies restored preferences to the account's
// calendars that exist and forgets them.
func applyPendingCalendars(tx *sql.Tx, accountID string) error {
	provider, email, err := accountKey(tx, accountID)
	if err != nil {
		return nil // account gone: nothing to apply
	}
	const pick = `FROM pending_calendars p WHERE p.provider = ?2 AND p.email = ?3 AND p.remote_id = calendars.remote_id`
	if _, err := tx.Exec(`UPDATE calendars SET visible = (SELECT p.visible `+pick+`), color_override = (SELECT p.color `+pick+`),
		muted = (SELECT p.muted `+pick+`)
		WHERE account_id = ?1 AND remote_id IN (SELECT remote_id FROM pending_calendars WHERE provider = ?2 AND email = ?3)`,
		accountID, provider, email); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM pending_calendars WHERE provider = ? AND email = ? AND remote_id IN
		(SELECT remote_id FROM calendars WHERE account_id = ?)`, provider, email, accountID)
	return err
}
