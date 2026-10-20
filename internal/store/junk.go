package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SenderMail lists the received mail of one mailbox whose sender (name or
// address) contains needle, for "Always send to Junk" (inboxOnly) and
// "Always label". Callers check the exact rule match; this only narrows
// the scan.
func (s *Store) SenderMail(accountID, needle string, inboxOnly bool) ([]RuleCandidate, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(needle)
	like := "%" + esc + "%"
	inbox := 0
	if inboxOnly {
		inbox = kindBits["inbox"]
	}
	args := append(ruleCandidatesArgs(nil), accountID, inbox, inbox, like, like)
	return s.scanRuleCandidates(nil, ruleCandidatesQuery+` AND account_id = ? AND (? = 0 OR (folder_kinds & ?) != 0)
		AND (from_email LIKE ? ESCAPE '\' OR from_name LIKE ? ESCAPE '\')`, args)
}

// RejectLabel records that the user doesn't want the label on these
// messages, so classification doesn't add it again ("Not junk"), and drops
// AI and manual assignments of it. Provider/system assignments are left to
// sync (the system Junk label goes when the message leaves Junk), so no
// override is recorded that would outlive the move.
func (s *Store) RejectLabel(ids []string, labelID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, chunk := range chunks(ids, 500) {
			args := append([]any{labelID}, anySlice(chunk)...)
			if _, err := tx.Exec(`DELETE FROM message_labels WHERE label_id = ? AND source IN ('ai', 'manual')
				AND message_id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO label_rejections(message_id, label_id, created_at)
				SELECT id, ?, `+fmt.Sprint(time.Now().UnixMilli())+` FROM messages WHERE id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
		}
		return nil
	})
}
