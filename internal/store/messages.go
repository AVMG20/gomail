package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

const summaryCols = `m.id, m.account_id, m.remote_id, m.thread_id, m.from_name, m.from_email, m.to_json, m.subject, m.snippet,
	m.date, m.unread, m.starred, m.has_attachments,
	coalesce((SELECT group_concat(mf.folder_id) FROM message_folders mf WHERE mf.message_id = m.id), ''),
	coalesce((SELECT group_concat(ml.label_id || '|' || ml.source || '|' || ml.confidence) FROM message_labels ml WHERE ml.message_id = m.id), '')`

func scanSummary(sc interface{ Scan(...any) error }, extra ...any) (models.MessageSummary, error) {
	var m models.MessageSummary
	var toJSON, folders, labels string
	var date int64
	var unread, starred, att int
	dest := []any{&m.ID, &m.AccountID, &m.RemoteID, &m.ThreadID, &m.From.Name, &m.From.Email, &toJSON, &m.Subject, &m.Snippet,
		&date, &unread, &starred, &att, &folders, &labels}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return m, err
	}
	m.Date = fromMillis(date)
	m.Unread, m.Starred, m.HasAttachments = unread == 1, starred == 1, att == 1
	m.To = decodeAddrs(toJSON)
	m.FolderIDs = []string{}
	if folders != "" {
		m.FolderIDs = strings.Split(folders, ",")
	}
	m.Labels, m.LabelDetails = decodeLabels(labels)
	return m, nil
}

func decodeLabels(s string) ([]string, []models.MessageLabel) {
	ids := []string{}
	details := []models.MessageLabel{}
	if s == "" {
		return ids, details
	}
	for _, part := range strings.Split(s, ",") {
		f := strings.SplitN(part, "|", 3)
		if len(f) != 3 {
			continue
		}
		conf, _ := strconv.ParseFloat(f[2], 64)
		ids = append(ids, f[0])
		details = append(details, models.MessageLabel{LabelID: f[0], Source: f[1], Confidence: conf})
	}
	sort.Strings(ids)
	sort.Slice(details, func(i, j int) bool { return details[i].LabelID < details[j].LabelID })
	return ids, details
}

func decodeAddrs(s string) []models.Address {
	out := []models.Address{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []models.Address{}
	}
	return out
}

func addrsJSON(a []models.Address) string {
	if a == nil {
		a = []models.Address{}
	}
	return mustJSON(a)
}

// ApplyResult reports what ApplySync changed.
type ApplyResult struct {
	// New lists local ids of messages that did not exist before.
	New []string
}

// ApplySync stores a provider sync round atomically: upserts, deletions,
// folder membership removals, orphan cleanup and the new sync state.
func (s *Store) ApplySync(accountID string, r mail.SyncResult) (ApplyResult, error) {
	var res ApplyResult
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		// Folder remote id -> local id (skipping "starred", which is
		// computed from the flag).
		folderIDs := map[string]string{}
		rows, err := tx.Query(`SELECT id, remote_id FROM folders WHERE account_id = ? AND kind != 'starred'`, accountID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, rid string
			if err := rows.Scan(&id, &rid); err != nil {
				rows.Close()
				return err
			}
			folderIDs[rid] = id
		}
		rows.Close()

		exists, err := tx.Prepare(`SELECT 1 FROM messages WHERE id = ?`)
		if err != nil {
			return err
		}
		defer exists.Close()
		up, err := tx.Prepare(`INSERT INTO messages(id, account_id, remote_id, thread_id, from_name, from_email, to_json, cc_json, bcc_json,
			reply_to_json, subject, snippet, date, unread, starred, has_attachments, message_id_hdr, in_reply_to, refs, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET thread_id = excluded.thread_id, from_name = excluded.from_name, from_email = excluded.from_email,
				to_json = excluded.to_json, cc_json = excluded.cc_json, bcc_json = excluded.bcc_json, reply_to_json = excluded.reply_to_json,
				subject = excluded.subject, snippet = excluded.snippet, date = excluded.date, unread = excluded.unread,
				starred = excluded.starred, has_attachments = excluded.has_attachments,
				message_id_hdr = CASE WHEN excluded.message_id_hdr != '' THEN excluded.message_id_hdr ELSE message_id_hdr END,
				in_reply_to = CASE WHEN excluded.in_reply_to != '' THEN excluded.in_reply_to ELSE in_reply_to END,
				refs = CASE WHEN excluded.refs != '' THEN excluded.refs ELSE refs END,
				updated_at = excluded.updated_at`)
		if err != nil {
			return err
		}
		defer up.Close()
		delMF, err := tx.Prepare(`DELETE FROM message_folders WHERE message_id = ?`)
		if err != nil {
			return err
		}
		defer delMF.Close()
		insMF, err := tx.Prepare(`INSERT OR IGNORE INTO message_folders(folder_id, message_id) VALUES(?, ?)`)
		if err != nil {
			return err
		}
		defer insMF.Close()

		now := time.Now().UnixMilli()
		for _, m := range r.Upserts {
			id := MessageID(accountID, m.RemoteID)
			var one int
			isNew := errors.Is(exists.QueryRow(id).Scan(&one), sql.ErrNoRows)
			if _, err := up.Exec(id, accountID, m.RemoteID, m.ThreadID, m.From.Name, m.From.Email, addrsJSON(m.To), addrsJSON(m.Cc),
				addrsJSON(m.Bcc), addrsJSON(m.ReplyTo), m.Subject, m.Snippet, toMillis(m.Date), boolInt(m.Unread), boolInt(m.Starred),
				boolInt(m.HasAttachments), m.MessageID, m.InReplyTo, m.References, now); err != nil {
				return err
			}
			if isNew {
				res.New = append(res.New, id)
			}
			if _, err := delMF.Exec(id); err != nil {
				return err
			}
			for _, fr := range m.FolderRemoteIDs {
				if fid, ok := folderIDs[fr]; ok {
					if _, err := insMF.Exec(fid, id); err != nil {
						return err
					}
				}
			}
		}
		for _, rid := range r.Deleted {
			if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, MessageID(accountID, rid)); err != nil {
				return err
			}
		}
		for fr, rids := range r.Removed {
			fid, ok := folderIDs[fr]
			if !ok {
				continue
			}
			for _, rid := range rids {
				if _, err := tx.Exec(`DELETE FROM message_folders WHERE folder_id = ? AND message_id = ?`, fid, MessageID(accountID, rid)); err != nil {
					return err
				}
			}
		}
		// Messages that are in no known folder any more are gone from
		// everything we sync.
		if _, err := tx.Exec(`DELETE FROM messages WHERE account_id = ? AND NOT EXISTS
			(SELECT 1 FROM message_folders mf WHERE mf.message_id = messages.id)`, accountID); err != nil {
			return err
		}
		if len(res.New) > 0 {
			// Orphan cleanup may have removed some "new" ids again.
			kept := res.New[:0]
			for _, id := range res.New {
				var one int
				if exists.QueryRow(id).Scan(&one) == nil {
					kept = append(kept, id)
				}
			}
			res.New = kept
		}
		if err := saveSyncState(tx, accountID, r.State); err != nil {
			return err
		}
		return recountStarred(tx, accountID)
	})
	return res, err
}

// Summaries returns summaries for the given ids (missing ids are skipped),
// in no particular order.
func (s *Store) Summaries(ids []string) ([]models.MessageSummary, error) {
	out := make([]models.MessageSummary, 0, len(ids))
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT `+summaryCols+` FROM messages m WHERE m.id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			m, err := scanSummary(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Message returns a message with its cached body. hasBody reports whether
// the body was cached.
func (s *Store) Message(id string) (msg models.Message, hasBody bool, err error) {
	var cc, bcc, replyTo string
	var html, text sql.NullString
	var remote int
	row := s.db.QueryRow(`SELECT `+summaryCols+`, m.cc_json, m.bcc_json, m.reply_to_json, m.message_id_hdr, m.in_reply_to, m.refs,
		m.body_html, m.body_text, m.has_remote FROM messages m WHERE m.id = ?`, id)
	sum, err := scanSummary(row, &cc, &bcc, &replyTo, &msg.MessageID, &msg.InReplyTo, &msg.References, &html, &text, &remote)
	if errors.Is(err, sql.ErrNoRows) {
		return msg, false, ErrNotFound
	}
	if err != nil {
		return msg, false, err
	}
	msg.MessageSummary = sum
	msg.Cc, msg.Bcc, msg.ReplyTo = decodeAddrs(cc), decodeAddrs(bcc), decodeAddrs(replyTo)
	msg.BodyHTML, msg.BodyText = html.String, text.String
	msg.HasRemoteContent = remote == 1
	msg.Attachments, err = s.attachments(id)
	return msg, html.Valid || text.Valid, err
}

func (s *Store) attachments(messageID string) ([]models.Attachment, error) {
	rows, err := s.db.Query(`SELECT id, filename, mime_type, size, inline, content_id FROM attachments WHERE message_id = ? ORDER BY rowid`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Attachment{}
	for rows.Next() {
		var a models.Attachment
		var inline int
		if err := rows.Scan(&a.ID, &a.Filename, &a.MimeType, &a.Size, &inline, &a.ContentID); err != nil {
			return nil, err
		}
		a.Inline = inline == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// Attachment returns one attachment's metadata.
func (s *Store) Attachment(messageID, attachmentID string) (models.Attachment, error) {
	var a models.Attachment
	var inline int
	err := s.db.QueryRow(`SELECT id, filename, mime_type, size, inline, content_id FROM attachments WHERE message_id = ? AND id = ?`,
		messageID, attachmentID).Scan(&a.ID, &a.Filename, &a.MimeType, &a.Size, &inline, &a.ContentID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Inline = inline == 1
	return a, err
}

// BodyUpdate is the content cached by SaveBody.
type BodyUpdate struct {
	HTML, Text       string
	HasRemoteContent bool
	Attachments      []models.Attachment
	MessageID        string
	InReplyTo        string
	References       string
	Cc, Bcc, ReplyTo []models.Address // only stored when non-empty
}

// SaveBody caches a message body and its attachment metadata.
func (s *Store) SaveBody(id string, b BodyUpdate) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE messages SET body_html = ?, body_text = ?, has_remote = ?, body_fetched_at = ?,
			has_attachments = CASE WHEN ? > 0 THEN 1 ELSE has_attachments END,
			message_id_hdr = CASE WHEN ? != '' THEN ? ELSE message_id_hdr END,
			in_reply_to = CASE WHEN ? != '' THEN ? ELSE in_reply_to END,
			refs = CASE WHEN ? != '' THEN ? ELSE refs END
			WHERE id = ?`,
			b.HTML, b.Text, boolInt(b.HasRemoteContent), time.Now().UnixMilli(), countNonInline(b.Attachments),
			b.MessageID, b.MessageID, b.InReplyTo, b.InReplyTo, b.References, b.References, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		for col, v := range map[string][]models.Address{"cc_json": b.Cc, "bcc_json": b.Bcc, "reply_to_json": b.ReplyTo} {
			if len(v) > 0 {
				if _, err := tx.Exec(`UPDATE messages SET `+col+` = ? WHERE id = ?`, addrsJSON(v), id); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM attachments WHERE message_id = ?`, id); err != nil {
			return err
		}
		for _, a := range b.Attachments {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO attachments(message_id, id, filename, mime_type, size, inline, content_id) VALUES(?,?,?,?,?,?,?)`,
				id, a.ID, a.Filename, a.MimeType, a.Size, boolInt(a.Inline), a.ContentID); err != nil {
				return err
			}
		}
		return nil
	})
}

func countNonInline(atts []models.Attachment) int {
	n := 0
	for _, a := range atts {
		if !a.Inline {
			n++
		}
	}
	return n
}

// Thread returns the messages of a thread in an account ordered by date.
func (s *Store) Thread(accountID, threadID string) ([]models.MessageSummary, error) {
	rows, err := s.db.Query(`SELECT `+summaryCols+` FROM messages m WHERE m.account_id = ? AND m.thread_id = ? ORDER BY m.date, m.id`,
		accountID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.MessageSummary
	for rows.Next() {
		m, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- local mutations (optimistic updates) ----

// SetUnread sets the unread flag and keeps folder unread counters in step.
// It returns the ids whose flag actually changed.
func (s *Store) SetUnread(ids []string, unread bool) ([]string, error) {
	var changed []string
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		delta := -1
		if unread {
			delta = 1
		}
		accounts := map[string]bool{}
		for _, id := range ids {
			var acct string
			err := tx.QueryRow(`UPDATE messages SET unread = ? WHERE id = ? AND unread != ? RETURNING account_id`,
				boolInt(unread), id, boolInt(unread)).Scan(&acct)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			changed = append(changed, id)
			accounts[acct] = true
			if _, err := tx.Exec(`UPDATE folders SET unread = max(0, unread + ?) WHERE kind != 'starred' AND id IN
				(SELECT folder_id FROM message_folders WHERE message_id = ?)`, delta, id); err != nil {
				return err
			}
		}
		for a := range accounts {
			if err := recountStarred(tx, a); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}

// SetStarred sets the starred flag; it returns the ids that changed.
func (s *Store) SetStarred(ids []string, starred bool) ([]string, error) {
	var changed []string
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		accounts := map[string]bool{}
		for _, id := range ids {
			var acct string
			err := tx.QueryRow(`UPDATE messages SET starred = ? WHERE id = ? AND starred != ? RETURNING account_id`,
				boolInt(starred), id, boolInt(starred)).Scan(&acct)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			changed = append(changed, id)
			accounts[acct] = true
		}
		for a := range accounts {
			if err := recountStarred(tx, a); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}

// MessageFolders returns the folder ids for each message.
func (s *Store) MessageFolders(ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT message_id, folder_id FROM message_folders WHERE message_id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m, f string
			if err := rows.Scan(&m, &f); err != nil {
				rows.Close()
				return nil, err
			}
			out[m] = append(out[m], f)
		}
		rows.Close()
	}
	return out, nil
}

// SetMessageFolders replaces the folder memberships of messages, adjusting
// folder totals/unread counters by the difference.
func (s *Store) SetMessageFolders(sets map[string][]string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for id, folders := range sets {
			var unread int
			if err := tx.QueryRow(`SELECT unread FROM messages WHERE id = ?`, id).Scan(&unread); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
			old := map[string]bool{}
			rows, err := tx.Query(`SELECT folder_id FROM message_folders WHERE message_id = ?`, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var f string
				if err := rows.Scan(&f); err != nil {
					rows.Close()
					return err
				}
				old[f] = true
			}
			rows.Close()
			want := map[string]bool{}
			for _, f := range folders {
				want[f] = true
			}
			for f := range old {
				if !want[f] {
					if _, err := tx.Exec(`DELETE FROM message_folders WHERE folder_id = ? AND message_id = ?`, f, id); err != nil {
						return err
					}
					if _, err := tx.Exec(`UPDATE folders SET total = max(0, total - 1), unread = max(0, unread - ?) WHERE id = ? AND kind != 'starred'`, unread, f); err != nil {
						return err
					}
				}
			}
			for f := range want {
				if !old[f] {
					if _, err := tx.Exec(`INSERT OR IGNORE INTO message_folders(folder_id, message_id) VALUES(?, ?)`, f, id); err != nil {
						return err
					}
					if _, err := tx.Exec(`UPDATE folders SET total = total + 1, unread = unread + ? WHERE id = ? AND kind != 'starred'`, unread, f); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// ---- labels ----

// AddLabels assigns a label manually (upgrading an existing AI assignment
// to manual). It returns the ids that exist.
func (s *Store) AddLabels(ids []string, labelID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at)
			SELECT id, ?, 'manual', 1, ? FROM messages WHERE id = ?
			ON CONFLICT(message_id, label_id) DO UPDATE SET source = 'manual', confidence = 1`)
		if err != nil {
			return err
		}
		defer st.Close()
		now := time.Now().UnixMilli()
		for _, id := range ids {
			if _, err := st.Exec(labelID, now, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveLabels removes a label (manual or AI) from messages.
func (s *Store) RemoveLabels(ids []string, labelID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, chunk := range chunks(ids, 500) {
			args := append([]any{labelID}, anySlice(chunk)...)
			if _, err := tx.Exec(`DELETE FROM message_labels WHERE label_id = ? AND message_id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// AIResult is one classifier outcome. LabelID "" means "no label applies".
type AIResult struct {
	ID         string
	LabelID    string
	Confidence float64
}

// ApplyAIResults replaces the previous AI labels of each message with the
// new result. Manual labels are never touched; if the AI picks a label the
// user already assigned manually it stays manual. The message is marked as
// classified either way.
func (s *Store) ApplyAIResults(results []AIResult) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		del, err := tx.Prepare(`DELETE FROM message_labels WHERE message_id = ? AND source = 'ai'`)
		if err != nil {
			return err
		}
		defer del.Close()
		ins, err := tx.Prepare(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at)
			SELECT id, ?, 'ai', ?, ? FROM messages WHERE id = ?
			ON CONFLICT(message_id, label_id) DO NOTHING`)
		if err != nil {
			return err
		}
		defer ins.Close()
		mark, err := tx.Prepare(`UPDATE messages SET classified_at = ? WHERE id = ?`)
		if err != nil {
			return err
		}
		defer mark.Close()
		now := time.Now().UnixMilli()
		for _, r := range results {
			if _, err := del.Exec(r.ID); err != nil {
				return err
			}
			if r.LabelID != "" {
				if _, err := ins.Exec(r.LabelID, r.Confidence, now, r.ID); err != nil {
					return err
				}
			}
			if _, err := mark.Exec(now, r.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUnknownLabels removes assignments of labels that no longer exist.
func (s *Store) DeleteUnknownLabels(valid []string) error {
	q := `DELETE FROM message_labels`
	if len(valid) > 0 {
		q += ` WHERE label_id NOT IN (` + placeholders(len(valid)) + `)`
	}
	_, err := s.db.Exec(q, anySlice(valid)...)
	return err
}

// LabelCounts counts labeled messages per label (total and unread),
// optionally restricted to one account. Messages in Trash are not
// counted.
func (s *Store) LabelCounts(accountID string) ([]models.LabelCount, error) {
	q := `SELECT ml.label_id, count(*), coalesce(sum(m.unread), 0)
		FROM message_labels ml JOIN messages m ON m.id = ml.message_id
		WHERE NOT EXISTS (SELECT 1 FROM message_folders mf JOIN folders f ON f.id = mf.folder_id
			WHERE mf.message_id = m.id AND f.kind = 'trash')`
	var args []any
	if accountID != "" {
		q += ` AND m.account_id = ?`
		args = append(args, accountID)
	}
	q += ` GROUP BY ml.label_id ORDER BY ml.label_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.LabelCount{}
	for rows.Next() {
		var c models.LabelCount
		if err := rows.Scan(&c.LabelID, &c.Total, &c.Unread); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClassifyInput is the data the classifier needs for one message.
type ClassifyInput struct {
	ID             string
	From           models.Address
	To             []models.Address
	Subject        string
	Snippet        string
	BodyText       string
	Date           time.Time
	HasAttachments bool
	FolderKinds    []string
	ClassifiedAt   time.Time
}

// ClassifyInputs loads classifier inputs for the given ids.
func (s *Store) ClassifyInputs(ids []string) ([]ClassifyInput, error) {
	var out []ClassifyInput
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT m.id, m.from_name, m.from_email, m.to_json, m.subject, m.snippet, coalesce(m.body_text, ''), m.date,
			m.has_attachments, m.classified_at,
			coalesce((SELECT group_concat(f.kind) FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE mf.message_id = m.id), '')
			FROM messages m WHERE m.id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c ClassifyInput
			var to string
			var date int64
			var att int
			var classified int64
			var kinds string
			if err := rows.Scan(&c.ID, &c.From.Name, &c.From.Email, &to, &c.Subject, &c.Snippet, &c.BodyText, &date, &att, &classified, &kinds); err != nil {
				rows.Close()
				return nil, err
			}
			c.To, c.Date, c.HasAttachments = decodeAddrs(to), fromMillis(date), att == 1
			c.ClassifiedAt = fromMillis(classified)
			if kinds != "" {
				c.FolderKinds = strings.Split(kinds, ",")
			}
			out = append(out, c)
		}
		rows.Close()
	}
	return out, nil
}

// ---- helpers ----

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func chunks(ss []string, n int) [][]string {
	var out [][]string
	for len(ss) > n {
		out = append(out, ss[:n])
		ss = ss[n:]
	}
	if len(ss) > 0 {
		out = append(out, ss)
	}
	return out
}
