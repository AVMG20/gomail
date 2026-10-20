package store

import (
	"context"
	"database/sql"

	"github.com/AVMG20/gomail/internal/config"
)

// MoveLabels moves a mailbox's label assignments to another label id
// (config.SplitLabels gave the mailbox its own copy of a shared label):
// assignments, AI and manual corrections, stored classifier scores and
// marks restored from a backup that wait for their messages.
func (s *Store) MoveLabels(moves []config.LabelMove) error {
	if len(moves) == 0 {
		return nil
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, m := range moves {
			if m.From == m.To || m.From == "" || m.To == "" {
				continue
			}
			inAccount := `message_id IN (SELECT id FROM messages WHERE account_id = ?)`
			for _, q := range []string{
				`UPDATE OR REPLACE message_labels SET label_id = ? WHERE label_id = ? AND ` + inAccount,
				`UPDATE OR REPLACE label_overrides SET label_id = ? WHERE label_id = ? AND ` + inAccount,
				`UPDATE OR REPLACE label_rejections SET label_id = ? WHERE label_id = ? AND ` + inAccount,
				`UPDATE OR REPLACE pending_labels SET label_id = ? WHERE label_id = ?
					AND (provider, email) IN (SELECT provider, lower(email) FROM accounts WHERE id = ?)`,
			} {
				if _, err := tx.Exec(q, m.To, m.From, m.AccountID); err != nil {
					return err
				}
			}
			// Stored scores are keyed by label id (they are reused for
			// mail that doesn't need classifying again).
			from, to := jsonKey(m.From), jsonKey(m.To)
			if _, err := tx.Exec(`UPDATE messages SET ai_scores = json_set(json_remove(ai_scores, ?), ?, json_extract(ai_scores, ?))
				WHERE account_id = ? AND ai_scores IS NOT NULL AND ai_scores != '' AND json_valid(ai_scores) AND json_type(ai_scores, ?) IS NOT NULL`,
				from, to, from, m.AccountID, from); err != nil {
				return err
			}
		}
		return nil
	})
}

// jsonKey is the JSON path of an object key (label ids are slugs: no
// quotes or backslashes).
func jsonKey(k string) string { return `$."` + k + `"` }

// RenameAIHashes points the "already classified" fingerprints of an
// account's mail from one label set to another (the label set changed in
// a way that doesn't change what the classifier is asked, like new ids for
// the same labels), so that mail isn't classified, and paid for, again.
// The fingerprint is "<labelset>:<message>".
func (s *Store) RenameAIHashes(accountID, from, to string) error {
	if from == to || from == "" || to == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE messages SET ai_hash = ? || substr(ai_hash, ?) WHERE account_id = ? AND substr(ai_hash, 1, ?) = ?`,
		to+":", len(from)+2, accountID, len(from)+1, from+":")
	return err
}

// DeleteAIAssignments removes the classifier's assignments of labels (the
// user's own and the provider's stay).
func (s *Store) DeleteAIAssignments(labelIDs []string) error {
	if len(labelIDs) == 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM message_labels WHERE source = 'ai' AND label_id IN (`+placeholders(len(labelIDs))+`)`, anySlice(labelIDs)...)
	return err
}
