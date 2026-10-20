package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// autoMovesKept is how long the log of automatic moves is kept.
const autoMovesKept = 30 * 24 * time.Hour

// AutoMoveEntry is one message an automation moved.
type AutoMoveEntry struct {
	MessageID, AccountID, ToKind, Reason string
}

// RecordAutoMoves logs automatic moves (and forgets entries older than 30 days).
func (s *Store) RecordAutoMoves(entries []AutoMoveEntry, at time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, e := range entries {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO auto_moves(message_id, account_id, to_kind, reason, created_at) VALUES(?,?,?,?,?)`,
				e.MessageID, e.AccountID, e.ToKind, e.Reason, toMillis(at)); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM auto_moves WHERE created_at < ?`, toMillis(at.Add(-autoMovesKept)))
		return err
	})
}

// AutoMoves lists what automations moved to a folder kind since a time,
// newest first ("" account = every mailbox). Only mail that is still there
// is listed, once (its latest move).
func (s *Store) AutoMoves(accountID, kind string, since time.Time, limit int) ([]models.AutoMove, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT a.message_id, a.account_id, a.to_kind, a.reason, max(a.created_at), m.from_name, m.from_email, m.subject, m.date
		FROM auto_moves a JOIN messages m ON m.id = a.message_id
		WHERE a.to_kind = ? AND a.created_at >= ? AND (? = '' OR a.account_id = ?) AND (m.folder_kinds & ?) != 0
		GROUP BY a.message_id ORDER BY max(a.created_at) DESC LIMIT ?`,
		kind, toMillis(since), accountID, accountID, kindBits[kind], limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AutoMove{}
	for rows.Next() {
		var m models.AutoMove
		var at, date int64
		if err := rows.Scan(&m.MessageID, &m.AccountID, &m.To, &m.Reason, &at, &m.FromName, &m.FromEmail, &m.Subject, &date); err != nil {
			return nil, err
		}
		m.At, m.Date = fromMillis(at), fromMillis(date)
		out = append(out, m)
	}
	return out, rows.Err()
}
