package store

import (
	"fmt"
	"strings"
)

// SenderStat is one sender address of a mailbox's folder ("Clean up by
// sender").
type SenderStat struct {
	AccountID string
	Address   string // lower-cased
	Name      string // of the newest message
	Count     int
	Unread    int
	Newest    int64 // unix ms
	NewestRid int64 // the newest message's rowid (List-Unsubscribe lookup)
}

// senderKindMask is the folder_kinds mask of a folder kind for sender
// counts: "" is the inbox, "all" is received mail outside Junk and Trash.
func senderKindMask(kind string) (int, error) {
	switch kind {
	case "", "inbox":
		return kindBits["inbox"], nil
	case "all":
		return kindMask([]string{"inbox", "archive", "custom"}), nil
	case "sent", "drafts":
		return 0, fmt.Errorf("no senders to clean up in %s", kind)
	}
	if b, ok := kindBits[kind]; ok {
		return b, nil
	}
	return 0, fmt.Errorf("unknown folder kind %q", kind)
}

// SenderStats counts the mail of a folder kind per sender address of each
// mailbox (accountID "" = every mailbox). Mail the mailbox sent itself and
// mail without a sender address are left out. It walks messages_senders
// in sender order and never reads message rows.
func (s *Store) SenderStats(accountID, kind string) ([]SenderStat, error) {
	mask, err := senderKindMask(kind)
	if err != nil {
		return nil, err
	}
	cond, args := `(m.folder_kinds & ?) != 0 AND m.from_email != ''`, []any{mask}
	if accountID != "" {
		cond, args = `m.account_id = ? AND `+cond, append([]any{accountID}, args...)
	}
	// One max() aggregate: SQLite takes the bare columns (name, rid) from
	// the newest message of each sender.
	rows, err := s.db.Query(`SELECT g.account_id, g.addr, g.name, g.n, g.unread, g.newest, g.rid FROM (
			SELECT m.account_id, lower(m.from_email) AS addr, m.from_name AS name, count(*) AS n, sum(m.unread) AS unread,
				max(m.date) AS newest, m.rid AS rid
			FROM messages m INDEXED BY messages_senders WHERE `+cond+`
			GROUP BY m.account_id, m.from_email COLLATE NOCASE
		) g JOIN accounts a ON a.id = g.account_id
		WHERE g.addr != lower(a.email)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SenderStat{}
	for rows.Next() {
		var st SenderStat
		if err := rows.Scan(&st.AccountID, &st.Address, &st.Name, &st.Count, &st.Unread, &st.Newest, &st.NewestRid); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// WithUnsubscribe returns which of these messages (by rowid) offer
// List-Unsubscribe.
func (s *Store) WithUnsubscribe(rids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for off := 0; off < len(rids); off += 500 {
		chunk := rids[off:min(off+500, len(rids))]
		args := make([]any, len(chunk))
		for i, r := range chunk {
			args[i] = r
		}
		rows, err := s.db.Query(`SELECT rid FROM messages WHERE rid IN (`+placeholders(len(chunk))+`)
			AND (unsub_url != '' OR unsub_mailto != '')`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rid int64
			if err := rows.Scan(&rid); err != nil {
				rows.Close()
				return nil, err
			}
			out[rid] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SenderMessageIDs lists the mail of one sender in a mailbox's folder kind,
// newest first: sender is an address (exact, any case) or "@domain" (every
// address at exactly that domain). Unlike a from: search it never matches
// look-alike addresses ("news@" vs "breaking.news@").
func (s *Store) SenderMessageIDs(accountID, kind, sender string) ([]string, error) {
	mask, err := senderKindMask(kind)
	if err != nil {
		return nil, err
	}
	sender = strings.ToLower(strings.TrimSpace(sender))
	at := strings.LastIndex(sender, "@")
	if accountID == "" || at < 0 || at == len(sender)-1 {
		return nil, fmt.Errorf("no sender to look up")
	}
	match, arg := `m.from_email = ? COLLATE NOCASE`, any(sender)
	if at == 0 {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(sender)
		match, arg = `m.from_email LIKE ? ESCAPE '\'`, "%"+esc
	}
	rows, err := s.db.Query(`SELECT m.id FROM messages m INDEXED BY messages_senders
		WHERE m.account_id = ? AND `+match+` AND (m.folder_kinds & ?) != 0
		ORDER BY m.date DESC, m.id DESC`, accountID, arg, mask)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
