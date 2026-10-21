package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

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
	// Changed reports whether the round changed any mail (rows, folders,
	// labels), as opposed to only advancing the sync state.
	Changed bool

	newWords []string
}

// ApplySync stores a provider sync round atomically: upserts, deletions,
// folder membership removals, orphan cleanup and the new sync state.
//
// labelIDs maps lower-cased provider label names to local label ids; names
// missing from it are ignored.
func (s *Store) ApplySync(accountID string, r mail.SyncResult, labelIDs map[string]string) (ApplyResult, error) {
	return s.ApplySyncKeeping(accountID, r, labelIDs, nil)
}

// ApplySyncKeeping is ApplySync, except that the messages in keep (local
// ids) keep their folder memberships against the round's folder removals
// and complete listings: they were moved locally after the round was
// fetched. Their upserts' folders still apply.
func (s *Store) ApplySyncKeeping(accountID string, r mail.SyncResult, labelIDs map[string]string, keep map[string]bool) (ApplyResult, error) {
	var res ApplyResult
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		// Row changes so far on this connection (unchanged upserts skip
		// their update, so they don't count).
		changes := func() int64 {
			var n int64
			_ = tx.QueryRow(`SELECT total_changes()`).Scan(&n)
			return n
		}
		start := changes()
		// Folder remote id -> local id (skipping "starred", which is
		// computed from the flag).
		folderIDs := map[string]string{}
		folderBits := map[string]int{}
		sentFolders := map[string]bool{}
		rows, err := tx.Query(`SELECT id, remote_id, kind FROM folders WHERE account_id = ? AND kind != 'starred'`, accountID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, rid, kind string
			if err := rows.Scan(&id, &rid, &kind); err != nil {
				rows.Close()
				return err
			}
			folderIDs[rid] = id
			folderBits[rid] = kindBits[kind]
			sentFolders[rid] = kind == "sent"
		}
		rows.Close()
		contact, err := tx.Prepare(`INSERT INTO contacts(email, name, count, last_seen) VALUES(?, ?, ?, ?)
			ON CONFLICT(email) DO UPDATE SET count = count + excluded.count, last_seen = max(last_seen, excluded.last_seen),
				name = CASE WHEN excluded.name != '' AND (name = '' OR excluded.last_seen >= last_seen) THEN excluded.name ELSE name END`)
		if err != nil {
			return err
		}
		defer contact.Close()

		exists, err := tx.Prepare(`SELECT 1 FROM messages WHERE id = ?`)
		if err != nil {
			return err
		}
		defer exists.Close()
		up, err := tx.Prepare(`INSERT INTO messages(id, account_id, remote_id, thread_id, from_name, from_email, to_json, cc_json, bcc_json,
			reply_to_json, subject, snippet, date, unread, starred, has_attachments, message_id_hdr, in_reply_to, refs, updated_at,
			unsub_url, unsub_mailto, unsub_one_click, folder_kinds)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			-- An update without a date only carries flags (a partial provider
			-- record): it never blanks the stored sender, subject and date.
			ON CONFLICT(id) DO UPDATE SET thread_id = CASE WHEN excluded.date = 0 THEN thread_id ELSE excluded.thread_id END,
				from_name = CASE WHEN excluded.date = 0 THEN from_name ELSE excluded.from_name END,
				from_email = CASE WHEN excluded.date = 0 THEN from_email ELSE excluded.from_email END,
				to_json = CASE WHEN excluded.date = 0 THEN to_json ELSE excluded.to_json END,
				cc_json = CASE WHEN excluded.date = 0 THEN cc_json ELSE excluded.cc_json END,
				bcc_json = CASE WHEN excluded.date = 0 THEN bcc_json ELSE excluded.bcc_json END,
				reply_to_json = CASE WHEN excluded.date = 0 THEN reply_to_json ELSE excluded.reply_to_json END,
				subject = CASE WHEN excluded.date = 0 THEN subject ELSE excluded.subject END,
				snippet = CASE WHEN excluded.date = 0 THEN snippet ELSE excluded.snippet END,
				date = CASE WHEN excluded.date = 0 THEN date ELSE excluded.date END, unread = excluded.unread,
				starred = excluded.starred, has_attachments = excluded.has_attachments,
				message_id_hdr = CASE WHEN excluded.message_id_hdr != '' THEN excluded.message_id_hdr ELSE message_id_hdr END,
				in_reply_to = CASE WHEN excluded.in_reply_to != '' THEN excluded.in_reply_to ELSE in_reply_to END,
				refs = CASE WHEN excluded.refs != '' THEN excluded.refs ELSE refs END,
				unsub_url = CASE WHEN excluded.unsub_url != '' OR excluded.unsub_mailto != '' THEN excluded.unsub_url ELSE unsub_url END,
				unsub_mailto = CASE WHEN excluded.unsub_url != '' OR excluded.unsub_mailto != '' THEN excluded.unsub_mailto ELSE unsub_mailto END,
				unsub_one_click = CASE WHEN excluded.unsub_url != '' OR excluded.unsub_mailto != '' THEN excluded.unsub_one_click ELSE unsub_one_click END,
				folder_kinds = excluded.folder_kinds,
				updated_at = excluded.updated_at
			-- Most of a sync round is unchanged mail: skip rewriting those
			-- rows (and their index entries).
			WHERE thread_id IS NOT excluded.thread_id OR from_name IS NOT excluded.from_name OR from_email IS NOT excluded.from_email
				OR to_json IS NOT excluded.to_json OR cc_json IS NOT excluded.cc_json OR bcc_json IS NOT excluded.bcc_json
				OR reply_to_json IS NOT excluded.reply_to_json OR subject IS NOT excluded.subject OR snippet IS NOT excluded.snippet
				OR date IS NOT excluded.date OR unread IS NOT excluded.unread OR starred IS NOT excluded.starred
				OR has_attachments IS NOT excluded.has_attachments OR folder_kinds IS NOT excluded.folder_kinds
				OR (excluded.message_id_hdr != '' AND message_id_hdr IS NOT excluded.message_id_hdr)
				OR (excluded.in_reply_to != '' AND in_reply_to IS NOT excluded.in_reply_to)
				OR (excluded.refs != '' AND refs IS NOT excluded.refs)
				OR ((excluded.unsub_url != '' OR excluded.unsub_mailto != '') AND (unsub_url IS NOT excluded.unsub_url
					OR unsub_mailto IS NOT excluded.unsub_mailto OR unsub_one_click IS NOT excluded.unsub_one_click))`)
		if err != nil {
			return err
		}
		defer up.Close()
		curMF, err := tx.Prepare(`SELECT folder_id FROM message_folders WHERE message_id = ?`)
		if err != nil {
			return err
		}
		defer curMF.Close()
		delMF, err := tx.Prepare(`DELETE FROM message_folders WHERE folder_id = ? AND message_id = ?`)
		if err != nil {
			return err
		}
		defer delMF.Close()
		insMF, err := tx.Prepare(`INSERT OR IGNORE INTO message_folders(folder_id, message_id) VALUES(?, ?)`)
		if err != nil {
			return err
		}
		defer insMF.Close()

		pl, err := newProviderLabeler(tx)
		if err != nil {
			return err
		}
		defer pl.close()
		kinds, err := newKindsUpdater(tx)
		if err != nil {
			return err
		}
		defer kinds.close()
		vins, err := tx.Prepare(`INSERT OR IGNORE INTO vocab(term) VALUES(?)`)
		if err != nil {
			return err
		}
		defer vins.Close()
		var newWords []string
		// Contacts and vocabulary are aggregated per batch and written once
		// per distinct address/word.
		type contactAgg struct {
			name   string
			weight int
			last   int64
		}
		contacts := map[string]*contactAgg{}
		addContact := func(a models.Address, weight int, date int64) {
			if a.Email == "" || !strings.Contains(a.Email, "@") {
				return
			}
			key := strings.ToLower(a.Email)
			c := contacts[key]
			if c == nil {
				c = &contactAgg{}
				contacts[key] = c
			}
			c.weight += weight
			if date >= c.last {
				c.last = date
				if a.Name != "" {
					c.name = a.Name
				}
			}
		}
		words := map[string]bool{}

		now := time.Now().UnixMilli()
		for _, m := range r.Upserts {
			id := MessageID(accountID, m.RemoteID)
			var one int
			isNew := errors.Is(exists.QueryRow(id).Scan(&one), sql.ErrNoRows)
			want := map[string]bool{}
			bits := 0
			for _, fr := range m.FolderRemoteIDs {
				if fid, ok := folderIDs[fr]; ok {
					want[fid] = true
					bits |= folderBits[fr]
				}
			}
			if _, err := up.Exec(id, accountID, m.RemoteID, m.ThreadID, m.From.Name, m.From.Email, addrsJSON(m.To), addrsJSON(m.Cc),
				addrsJSON(m.Bcc), addrsJSON(m.ReplyTo), m.Subject, m.Snippet, toMillis(m.Date), boolInt(m.Unread), boolInt(m.Starred),
				boolInt(m.HasAttachments), m.MessageID, m.InReplyTo, m.References, now,
				m.Unsubscribe.URL, m.Unsubscribe.Mailto, boolInt(m.Unsubscribe.OneClick), bits); err != nil {
				return err
			}
			if isNew {
				res.New = append(res.New, id)
				sent := false
				for _, fr := range m.FolderRemoteIDs {
					sent = sent || sentFolders[fr]
				}
				date := toMillis(m.Date)
				weight := 1
				if sent {
					weight = 5
				} else {
					addContact(m.From, 1, date)
				}
				for _, a := range m.To {
					addContact(a, weight, date)
				}
				for _, a := range m.Cc {
					addContact(a, weight, date)
				}
			} else {
				// Only apply membership differences (no churn in counters).
				have, err := queryStrings(curMF, id)
				if err != nil {
					return err
				}
				for fid := range have {
					if !want[fid] {
						if _, err := delMF.Exec(fid, id); err != nil {
							return err
						}
					} else {
						delete(want, fid)
					}
				}
			}
			for fid := range want {
				if _, err := insMF.Exec(fid, id); err != nil {
					return err
				}
			}
			for _, vw := range vocabWords(m) {
				words[vw] = true
			}
			var lids []string
			for _, name := range m.Labels {
				if lid, ok := labelIDs[strings.ToLower(name)]; ok {
					lids = append(lids, lid)
				}
			}
			if isNew {
				if err := pl.setNew(id, lids, now); err != nil {
					return err
				}
			} else if err := pl.set(id, lids, now); err != nil {
				return err
			}
		}
		for email, c := range contacts {
			if _, err := contact.Exec(email, c.name, c.weight, c.last); err != nil {
				return err
			}
		}
		for w := range words {
			s.vocabMu.Lock()
			known := s.vocab != nil && s.vocab.seen[w]
			s.vocabMu.Unlock()
			if known {
				continue
			}
			if r, err := vins.Exec(w); err != nil {
				return err
			} else if n, _ := r.RowsAffected(); n > 0 {
				newWords = append(newWords, w)
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
				mid := MessageID(accountID, rid)
				if keep[mid] {
					continue
				}
				if _, err := tx.Exec(`DELETE FROM message_folders WHERE folder_id = ? AND message_id = ?`, fid, mid); err != nil {
					return err
				}
				if err := kinds.update(mid); err != nil {
					return err
				}
			}
		}
		// Touched messages that are in no known folder any more are gone
		// from everything we sync.
		touched := make([]string, 0, len(r.Upserts))
		for _, m := range r.Upserts {
			touched = append(touched, MessageID(accountID, m.RemoteID))
		}
		for _, rids := range r.Removed {
			for _, rid := range rids {
				touched = append(touched, MessageID(accountID, rid))
			}
		}
		// A full listing of a folder (from a date on) also tells which
		// cached messages left it while we couldn't follow changes (e.g.
		// expired history after a long time offline).
		res.Changed = changes() > start
		// (reconcileComplete also saves cursors: count only what it moved.)
		left, err := reconcileComplete(tx, accountID, r, folderIDs, kinds, keep)
		if err != nil {
			return err
		}
		res.Changed = res.Changed || len(left) > 0
		touched = append(touched, left...)
		before := changes()
		for _, chunk := range chunks(touched, 400) {
			if err := keepOrphanMarks(tx, chunk); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM messages WHERE id IN (`+placeholders(len(chunk))+`) AND NOT EXISTS
				(SELECT 1 FROM message_folders mf WHERE mf.message_id = messages.id)`, anySlice(chunk)...); err != nil {
				return err
			}
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
		res.Changed = res.Changed || changes() > before
		if err := saveSyncState(tx, accountID, r.State); err != nil {
			return err
		}
		// Label marks restored from a backup, waiting for these messages.
		if n, err := applyPendingLabels(tx, accountID, now); err != nil {
			return err
		} else if n > 0 {
			res.Changed = true
		}
		before = changes()
		if err := refreshJunk(tx, touched, now); err != nil {
			return err
		}
		res.Changed = res.Changed || changes() > before
		if err := recountStarred(tx, accountID); err != nil {
			return err
		}
		res.newWords = newWords
		return nil
	})
	if err == nil && len(res.newWords) > 0 {
		s.vocabMu.Lock()
		if v := s.vocab; v != nil {
			v.mu.Lock()
			for _, w := range res.newWords {
				v.add(w)
			}
			v.mu.Unlock()
		}
		s.vocabMu.Unlock()
	}
	res.newWords = nil
	return res, err
}

// keepOrphanMarks saves the user's label decisions (manual labels,
// rejections, overrides) of the given messages that are in no folder any
// more as pending label marks, before orphan cleanup deletes them. A
// message can leave every synced folder without being deleted (e.g. an
// old message another Outlook client moved: folder deltas only cover
// recent mail), and when it comes back the marks apply again.
func keepOrphanMarks(tx *sql.Tx, ids []string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO pending_labels(provider, email, remote_id, label_id, kind, confidence)
		WITH gone AS (SELECT m.id, m.remote_id, a.provider, a.email FROM messages m JOIN accounts a ON a.id = m.account_id
			WHERE m.id IN (`+placeholders(len(ids))+`)
			AND NOT EXISTS (SELECT 1 FROM message_folders mf WHERE mf.message_id = m.id))
		SELECT g.provider, g.email, g.remote_id, ml.label_id, '`+MarkManual+`', 1 FROM gone g
			JOIN message_labels ml ON ml.message_id = g.id AND ml.source = 'manual'
		UNION ALL SELECT g.provider, g.email, g.remote_id, r.label_id, '`+MarkRejected+`', 1 FROM gone g
			JOIN label_rejections r ON r.message_id = g.id
		UNION ALL SELECT g.provider, g.email, g.remote_id, o.label_id, '`+MarkOverride+`', 1 FROM gone g
			JOIN label_overrides o ON o.message_id = g.id`, anySlice(ids)...)
	return err
}

// reconcileComplete drops folder memberships that a complete listing
// (r.Complete) no longer reports, and seeds the older-mail cursors. It
// returns the ids of the messages it changed.
func reconcileComplete(tx *sql.Tx, accountID string, r mail.SyncResult, folderIDs map[string]string, kinds *kindsUpdater, keep map[string]bool) ([]string, error) {
	if len(r.Complete) == 0 {
		return nil, nil
	}
	listed := make(map[string]bool, len(r.Upserts))
	for _, m := range r.Upserts {
		listed[MessageID(accountID, m.RemoteID)] = true
	}
	var changed []string
	state := map[string]string{}
	for fr, since := range r.Complete {
		fid, ok := folderIDs[fr]
		if !ok {
			continue
		}
		from := int64(math.MinInt64)
		if !since.IsZero() {
			from = toMillis(since)
		}
		ids, err := queryList(tx, `SELECT mf.message_id FROM message_folders mf JOIN messages m ON m.id = mf.message_id
			WHERE mf.folder_id = ? AND m.date > ?`, fid, from)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if listed[id] || keep[id] {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM message_folders WHERE folder_id = ? AND message_id = ?`, fid, id); err != nil {
				return nil, err
			}
			if err := kinds.update(id); err != nil {
				return nil, err
			}
			changed = append(changed, id)
		}
		if since.IsZero() {
			state[olderDoneKey(fid)] = "1"
			continue
		}
		var cur string
		err = tx.QueryRow(`SELECT value FROM sync_state WHERE account_id = ? AND key = ?`, accountID, olderCursorKey(fid)).Scan(&cur)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// Keep a deeper cursor from earlier backfilling.
		if c, err := strconv.ParseInt(cur, 10, 64); err != nil || c > toMillis(since) {
			state[olderCursorKey(fid)] = strconv.FormatInt(toMillis(since), 10)
		}
	}
	return changed, saveSyncState(tx, accountID, state)
}

func queryList(tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// providerLabeler replaces the provider-sourced label assignments of a
// message, honoring AI overrides.
type providerLabeler struct {
	cur, del, ins, ovr, clearOvr *sql.Stmt
}

func newProviderLabeler(tx *sql.Tx) (*providerLabeler, error) {
	p := &providerLabeler{}
	var err error
	prep := func(q string) *sql.Stmt {
		if err != nil {
			return nil
		}
		var st *sql.Stmt
		st, err = tx.Prepare(q)
		return st
	}
	p.cur = prep(`SELECT label_id FROM message_labels WHERE message_id = ? AND source = 'provider'`)
	p.del = prep(`DELETE FROM message_labels WHERE message_id = ? AND label_id = ? AND source = 'provider'`)
	p.ins = prep(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at) VALUES(?, ?, 'provider', 1, ?)
		ON CONFLICT(message_id, label_id) DO UPDATE SET source = 'provider', confidence = 1 WHERE source = 'ai'`)
	p.ovr = prep(`SELECT label_id FROM label_overrides WHERE message_id = ?`)
	p.clearOvr = prep(`DELETE FROM label_overrides WHERE message_id = ? AND label_id = ?`)
	if err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func (p *providerLabeler) close() {
	for _, st := range []*sql.Stmt{p.cur, p.del, p.ins, p.ovr, p.clearOvr} {
		if st != nil {
			st.Close()
		}
	}
}

func queryStrings(st *sql.Stmt, args ...any) (map[string]bool, error) {
	rows, err := st.Query(args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// setNew assigns provider labels to a message that has no labels yet.
func (p *providerLabeler) setNew(msgID string, labelIDs []string, now int64) error {
	for _, l := range labelIDs {
		if l == "" || l == junkID {
			continue
		}
		if _, err := p.ins.Exec(msgID, l, now); err != nil {
			return err
		}
	}
	return nil
}

func (p *providerLabeler) set(msgID string, labelIDs []string, now int64) error {
	want := map[string]bool{}
	for _, l := range labelIDs {
		if l != "" && l != junkID {
			want[l] = true
		}
	}
	cur, err := queryStrings(p.cur, msgID)
	if err != nil {
		return err
	}
	ovr, err := queryStrings(p.ovr, msgID)
	if err != nil {
		return err
	}
	for l := range cur {
		if !want[l] {
			if _, err := p.del.Exec(msgID, l); err != nil {
				return err
			}
		}
	}
	for l := range ovr {
		if l != junkID && !want[l] {
			// The provider dropped the label: forget the AI override so a
			// later re-add by the provider applies again.
			if _, err := p.clearOvr.Exec(msgID, l); err != nil {
				return err
			}
		}
	}
	for l := range want {
		if ovr[l] || cur[l] {
			continue
		}
		if _, err := p.ins.Exec(msgID, l, now); err != nil {
			return err
		}
	}
	return nil
}

const junkID = "junk"

const inSpam = `(%s & 16) != 0`

// refreshJunk gives every message in a Spam/Junk folder the system "junk"
// label and removes it from messages that left spam. It only looks at the
// given messages, so syncs don't scan the whole mailbox.
func refreshJunk(tx *sql.Tx, ids []string, now int64) error {
	for _, chunk := range chunks(ids, 400) {
		in := placeholders(len(chunk))
		args := anySlice(chunk)
		if _, err := tx.Exec(`DELETE FROM message_labels WHERE label_id = 'junk' AND source = 'system' AND message_id IN
			(SELECT m.id FROM messages m WHERE m.id IN (`+in+`) AND NOT `+fmt.Sprintf(inSpam, "m.folder_kinds")+`)`, args...); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM label_overrides WHERE label_id = 'junk' AND message_id IN
			(SELECT m.id FROM messages m WHERE m.id IN (`+in+`) AND NOT `+fmt.Sprintf(inSpam, "m.folder_kinds")+`)`, args...); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at)
			SELECT m.id, 'junk', 'system', 1, ? FROM messages m WHERE m.id IN (`+in+`) AND `+fmt.Sprintf(inSpam, "m.folder_kinds")+`
			AND NOT EXISTS (SELECT 1 FROM label_overrides o WHERE o.message_id = m.id AND o.label_id = 'junk')
			ON CONFLICT(message_id, label_id) DO UPDATE SET source = 'system', confidence = 1 WHERE source = 'ai'`,
			append([]any{now}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

// ProviderLabels returns the labels Gmail or Outlook set on each message
// (labels and categories that came with the mail).
func (s *Store) ProviderLabels(ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT message_id, label_id FROM message_labels WHERE source = 'provider' AND message_id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, l string
			if err := rows.Scan(&id, &l); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = append(out[id], l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AllLabelIDs returns every label on a message, hidden imported labels
// included (SetHideProviderLabels).
func (s *Store) AllLabelIDs(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT label_id FROM message_labels WHERE message_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Summaries returns summaries for the given ids (missing ids are skipped),
// in no particular order.
func (s *Store) Summaries(ids []string) ([]models.MessageSummary, error) {
	out := make([]models.MessageSummary, 0, len(ids))
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT `+s.summaryCols()+` FROM messages m WHERE m.id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
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
	var remote, oneClick, kinds int
	var unsubURL, unsubMailto, invite string
	row := s.db.QueryRow(`SELECT `+s.summaryCols()+`, m.cc_json, m.bcc_json, m.reply_to_json, m.message_id_hdr, m.in_reply_to, m.refs,
		m.body_html, m.body_text, m.has_remote, m.unsub_url, m.unsub_mailto, m.unsub_one_click, m.folder_kinds, m.invite FROM messages m WHERE m.id = ?`, id)
	sum, err := scanSummary(row, &cc, &bcc, &replyTo, &msg.MessageID, &msg.InReplyTo, &msg.References, &html, &text, &remote,
		&unsubURL, &unsubMailto, &oneClick, &kinds, &invite)
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
	msg.IsDraft = kinds&kindBits["drafts"] != 0
	if unsubURL != "" || unsubMailto != "" {
		msg.Unsubscribe = &models.Unsubscribe{URL: unsubURL, Mailto: unsubMailto, OneClick: oneClick == 1 && unsubURL != ""}
	}
	if invite != "" {
		var inv models.Invite
		if json.Unmarshal([]byte(invite), &inv) == nil {
			msg.Invite = &inv
		}
	}
	msg.ThreadCount = 1
	msg.Participants = []string{displayName(sum.From)}
	msg.Attachments, err = s.attachments(id)
	return msg, html.Valid || text.Valid, err
}

func (s *Store) attachments(messageID string) ([]models.Attachment, error) {
	rows, err := s.db.Query(`SELECT id, filename, mime_type, size, inline, content_id FROM attachments WHERE message_id = ? ORDER BY pos`, messageID)
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
	Unsubscribe      mail.Unsubscribe // only stored when non-empty
	Invite           *models.Invite   // calendar invitation, if any
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
		invite := ""
		if b.Invite != nil {
			j, _ := json.Marshal(b.Invite)
			invite = string(j)
		}
		if _, err := tx.Exec(`UPDATE messages SET invite = ? WHERE id = ?`, invite, id); err != nil {
			return err
		}
		if !b.Unsubscribe.Empty() {
			if _, err := tx.Exec(`UPDATE messages SET unsub_url = ?, unsub_mailto = ?, unsub_one_click = ? WHERE id = ?`,
				b.Unsubscribe.URL, b.Unsubscribe.Mailto, boolInt(b.Unsubscribe.OneClick), id); err != nil {
				return err
			}
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
		for i, a := range b.Attachments {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO attachments(message_id, id, filename, mime_type, size, inline, content_id, pos) VALUES(?,?,?,?,?,?,?,?)`,
				id, a.ID, stripBidi(a.Filename), a.MimeType, a.Size, boolInt(a.Inline), a.ContentID, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// stripBidi drops bidi controls, which can make "invoice\u202Efdp.exe"
// display as "invoiceexe.pdf".
func stripBidi(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
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

// Thread returns the messages of a thread in an account ordered by date
// (without messages in no folder, which are being deleted).
func (s *Store) Thread(accountID, threadID string) ([]models.MessageSummary, error) {
	rows, err := s.db.Query(`SELECT `+s.summaryCols()+` FROM messages m WHERE m.account_id = ? AND m.thread_id = ? AND `+visible("m")+`
		ORDER BY m.date, m.id`,
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
		var moved []string
		accounts := map[string]bool{}
		for id, folders := range sets {
			var unread int
			var acct string
			if err := tx.QueryRow(`SELECT unread, account_id FROM messages WHERE id = ?`, id).Scan(&unread, &acct); err != nil {
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
			if err := recomputeKinds(tx, id, ""); err != nil {
				return err
			}
			moved = append(moved, id)
			accounts[acct] = true
		}
		// Messages left in no folder (emptied) leave the starred view.
		for a := range accounts {
			if err := recountStarred(tx, a); err != nil {
				return err
			}
		}
		return refreshJunk(tx, moved, time.Now().UnixMilli())
	})
}

// ---- labels ----

// AddLabels assigns a label manually (upgrading an existing AI, provider or
// system assignment to manual).
func (s *Store) AddLabels(ids []string, labelID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at)
			SELECT id, ?, 'manual', 1, ? FROM messages WHERE id = ?
			ON CONFLICT(message_id, label_id) DO UPDATE SET source = 'manual', confidence = 1`)
		if err != nil {
			return err
		}
		defer st.Close()
		unreject, err := tx.Prepare(`DELETE FROM label_rejections WHERE message_id = ? AND label_id = ?`)
		if err != nil {
			return err
		}
		defer unreject.Close()
		now := time.Now().UnixMilli()
		for _, id := range ids {
			if _, err := st.Exec(labelID, now, id); err != nil {
				return err
			}
			if _, err := unreject.Exec(id, labelID); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveLabels removes a label from messages whatever its source. Removing
// a provider/system assignment records an override so sync doesn't re-add
// it, and every removal records a rejection so classification doesn't.
//
// Manual assignments get an override too: AddLabels upgrades a provider or
// system assignment to manual, so a manual label may still be reported by
// the provider (or be Junk of a message in Spam). Sync drops the override
// once the provider stops reporting the label (refreshJunk once the message
// leaves Spam), so it is harmless for labels that were always manual.
func (s *Store) RemoveLabels(ids []string, labelID string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, chunk := range chunks(ids, 500) {
			args := append([]any{labelID}, anySlice(chunk)...)
			if _, err := tx.Exec(`INSERT OR REPLACE INTO label_overrides(message_id, label_id, created_at)
				SELECT message_id, label_id, `+fmt.Sprint(time.Now().UnixMilli())+` FROM message_labels
				WHERE label_id = ? AND source IN ('provider', 'system', 'manual') AND message_id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM message_labels WHERE label_id = ? AND message_id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
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

// AIPolicy controls how classifier scores change label assignments.
type AIPolicy struct {
	AddThreshold    float64 // add (or keep) an AI label when P >= this
	RemoveThreshold float64 // remove provider/system labels when P < this ...
	CorrectProvider bool    // ... if enabled
}

// ApplyAIScores updates the labels of one message from classifier scores
// (label id -> P(yes)):
//   - manual assignments are never touched;
//   - AI assignments are kept when P >= AddThreshold, otherwise removed
//     (re-classifying replaces previous AI decisions);
//   - provider/system assignments are removed when CorrectProvider and
//     P < RemoveThreshold, recording an override so the next sync doesn't
//     re-add them;
//   - unassigned labels are added with source "ai" when P >= AddThreshold,
//     unless the user removed that label from the message by hand.
//
// Labels missing from scores are left alone. It returns the added and
// removed label ids.
//
// hash fingerprints the message content and label definitions; together
// with the scores it is stored so unchanged messages aren't re-classified.
func (s *Store) ApplyAIScores(msgID string, scores map[string]float64, p AIPolicy, hash string) (added, removed []string, err error) {
	added, removed = []string{}, []string{}
	err = s.tx(context.Background(), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT label_id, source FROM message_labels WHERE message_id = ?`, msgID)
		if err != nil {
			return err
		}
		cur := map[string]string{}
		for rows.Next() {
			var l, src string
			if err := rows.Scan(&l, &src); err != nil {
				rows.Close()
				return err
			}
			cur[l] = src
		}
		rows.Close()
		rejected, err := queryList(tx, `SELECT label_id FROM label_rejections WHERE message_id = ?`, msgID)
		if err != nil {
			return err
		}
		isRejected := map[string]bool{}
		for _, l := range rejected {
			isRejected[l] = true
		}
		now := time.Now().UnixMilli()
		ids := make([]string, 0, len(scores))
		for l := range scores {
			ids = append(ids, l)
		}
		sort.Strings(ids)
		for _, l := range ids {
			pr := scores[l]
			switch src, ok := cur[l]; {
			case !ok:
				if pr >= p.AddThreshold && !isRejected[l] { // never re-add what the user removed
					if _, err := tx.Exec(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at) VALUES(?, ?, 'ai', ?, ?)`,
						msgID, l, pr, now); err != nil {
						return err
					}
					added = append(added, l)
				}
			case src == "manual":
			case src == "ai":
				if pr >= p.AddThreshold {
					_, err = tx.Exec(`UPDATE message_labels SET confidence = ? WHERE message_id = ? AND label_id = ?`, pr, msgID, l)
				} else {
					_, err = tx.Exec(`DELETE FROM message_labels WHERE message_id = ? AND label_id = ?`, msgID, l)
					removed = append(removed, l)
				}
				if err != nil {
					return err
				}
			default: // provider, system
				if p.CorrectProvider && pr < p.RemoveThreshold {
					if _, err := tx.Exec(`DELETE FROM message_labels WHERE message_id = ? AND label_id = ?`, msgID, l); err != nil {
						return err
					}
					if _, err := tx.Exec(`INSERT OR REPLACE INTO label_overrides(message_id, label_id, created_at) VALUES(?, ?, ?)`, msgID, l, now); err != nil {
						return err
					}
					removed = append(removed, l)
				}
			}
		}
		_, err = tx.Exec(`UPDATE messages SET classified_at = ?, ai_hash = ?, ai_scores = ? WHERE id = ?`, now, hash, mustJSON(scores), msgID)
		return err
	})
	return added, removed, err
}

// DeleteUnknownLabels removes assignments (and overrides) of labels that no
// longer exist.
func (s *Store) DeleteUnknownLabels(valid []string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		for _, table := range []string{"message_labels", "label_overrides", "label_rejections"} {
			q := `DELETE FROM ` + table
			if len(valid) > 0 {
				q += ` WHERE label_id NOT IN (` + placeholders(len(valid)) + `)`
			}
			if _, err := tx.Exec(q, anySlice(valid)...); err != nil {
				return err
			}
		}
		q := `DELETE FROM pending_labels WHERE label_id != ''`
		if len(valid) > 0 {
			q += ` AND label_id NOT IN (` + placeholders(len(valid)) + `)`
		}
		_, err := tx.Exec(q, anySlice(valid)...)
		return err
	})
}

// LabelCounts counts labeled messages per label (total and unread),
// optionally restricted to one account. Messages in Trash are not
// counted.
func (s *Store) LabelCounts(accountID string) ([]models.LabelCount, error) {
	// message_labels carries denormalized account_id/unread/trashed columns
	// (kept current by triggers), so this is an index-only scan.
	q := `SELECT ml.label_id, count(*), coalesce(sum(ml.unread), 0) FROM message_labels ml WHERE ml.trashed = 0` + s.labelCond()
	var args []any
	if accountID != "" {
		q += ` AND ml.account_id = ?`
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

// ViewLabelCounts counts labeled messages per label (total and unread)
// within the box q shows (its account and folder, or folder kind across
// accounts), so the sidebar matches what clicking a label filters. The
// list's own filters (labels, search, unread only) are ignored. Trash only
// counts in the Trash view.
func (s *Store) ViewLabelCounts(q models.ListQuery) ([]models.LabelCount, error) {
	w, err := s.buildFilter(models.ListQuery{AccountID: q.AccountID, FolderID: q.FolderID, FolderKind: q.FolderKind}, FilterOptions{})
	if err != nil {
		return nil, err
	}
	if !s.isTrashView(q) {
		w.add(`ml.trashed = 0`)
	}
	w.add(`1` + s.labelCond()) // labelCond starts with " AND"
	// CROSS JOIN keeps messages outer: the box's messages come from a
	// covering list index; label-first read every labeled message's row
	// (past its cached body) to test the box.
	rows, err := s.db.Query(`SELECT ml.label_id, count(*), coalesce(sum(ml.unread), 0)
		FROM messages m CROSS JOIN message_labels ml ON ml.message_id = m.id`+w.sql()+` GROUP BY ml.label_id ORDER BY ml.label_id`, w.args...)
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

func (s *Store) isTrashView(q models.ListQuery) bool {
	if q.FolderID != "" {
		f, err := s.Folder(q.FolderID)
		return err == nil && f.Kind == models.FolderTrash
	}
	return q.FolderKind == models.FolderTrash
}

// LabelAccounts returns, per label, the accounts whose messages carry it
// (used to give provider labels from before labels were per mailbox the
// mailboxes they were imported in).
func (s *Store) LabelAccounts() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT label_id, account_id FROM message_labels WHERE account_id != '' ORDER BY label_id, account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var l, a string
		if err := rows.Scan(&l, &a); err != nil {
			return nil, err
		}
		out[l] = append(out[l], a)
	}
	return out, rows.Err()
}

// ClassifyInput is the data the classifier needs for one message.
type ClassifyInput struct {
	ID             string
	AccountID      string
	From           models.Address
	To             []models.Address
	Subject        string
	Snippet        string
	BodyText       string
	Date           time.Time
	HasAttachments bool
	FolderKinds    []string
	ClassifiedAt   time.Time
	AIHash         string             // fingerprint of the last AI run
	AIScores       map[string]float64 // scores of the last AI run
}

// MessagePlace is the mailbox of a message and the kinds of the folders
// it is in.
type MessagePlace struct {
	AccountID string
	Kinds     []string
}

// FolderKinds returns where each message is (from the denormalized
// folder_kinds bits: no body or folder rows read).
func (s *Store) FolderKinds(ids []string) (map[string]MessagePlace, error) {
	out := make(map[string]MessagePlace, len(ids))
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT id, account_id, folder_kinds FROM messages WHERE id IN (`+placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, account string
			var bits int
			if err := rows.Scan(&id, &account, &bits); err != nil {
				rows.Close()
				return nil, err
			}
			kinds := []string{}
			for k, b := range kindBits {
				if bits&b != 0 {
					kinds = append(kinds, k)
				}
			}
			out[id] = MessagePlace{account, kinds}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ClassifyInputs loads classifier inputs for the given ids.
func (s *Store) ClassifyInputs(ids []string) ([]ClassifyInput, error) {
	var out []ClassifyInput
	for _, chunk := range chunks(ids, 500) {
		rows, err := s.db.Query(`SELECT m.id, m.account_id, m.from_name, m.from_email, m.to_json, m.subject, m.snippet, coalesce(m.body_text, ''), m.date,
			m.has_attachments, m.classified_at, m.ai_hash, m.ai_scores,
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
			var kinds, scores string
			if err := rows.Scan(&c.ID, &c.AccountID, &c.From.Name, &c.From.Email, &to, &c.Subject, &c.Snippet, &c.BodyText, &date, &att, &classified,
				&c.AIHash, &scores, &kinds); err != nil {
				rows.Close()
				return nil, err
			}
			c.To, c.Date, c.HasAttachments = decodeAddrs(to), fromMillis(date), att == 1
			c.ClassifiedAt = fromMillis(classified)
			if scores != "" {
				_ = json.Unmarshal([]byte(scores), &c.AIScores)
			}
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

// RecordAIUsage stores one OpenRouter call.
func (s *Store) RecordAIUsage(kind, model string, cost float64, in, out int) error {
	_, err := s.db.Exec(`INSERT INTO ai_usage(at, kind, model, cost, input_tokens, output_tokens) VALUES(?,?,?,?,?,?)`,
		time.Now().UnixMilli(), kind, model, cost, in, out)
	return err
}

// AIUsage sums recorded spending for today and this month (local time).
func (s *Store) AIUsage(now time.Time) (models.AIUsage, error) {
	var u models.AIUsage
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	err := s.db.QueryRow(`SELECT coalesce(sum(CASE WHEN at >= ? THEN cost END), 0), coalesce(sum(CASE WHEN at >= ? THEN 1 END), 0),
		coalesce(sum(cost), 0), count(*) FROM ai_usage WHERE at >= ?`, day.UnixMilli(), day.UnixMilli(), month.UnixMilli()).
		Scan(&u.TodayUSD, &u.RequestsToday, &u.MonthUSD, &u.RequestsMonth)
	return u, err
}

// ApplyRuleLabel gives messages a label from a rule (stored like a manual
// label, so classification leaves it alone). Messages that already have it,
// or where the user removed it by hand, are skipped; an AI assignment of
// the label becomes a rule (manual) one, so a later re-classification
// can't take away what the rule gave. Returns the ids that got the label.
func (s *Store) ApplyRuleLabel(ids []string, labelID string) ([]string, error) {
	var added []string
	err := s.tx(context.Background(), func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		for _, chunk := range chunks(ids, 500) {
			if _, err := tx.Exec(`UPDATE message_labels SET source = 'manual', confidence = 1
				WHERE label_id = ? AND source = 'ai' AND message_id IN (`+placeholders(len(chunk))+`)`,
				append([]any{labelID}, anySlice(chunk)...)...); err != nil {
				return err
			}
			rows, err := tx.Query(`INSERT INTO message_labels(message_id, label_id, source, confidence, created_at)
				SELECT m.id, ?, 'manual', 1, ? FROM messages m
				WHERE m.id IN (`+placeholders(len(chunk))+`)
				AND NOT EXISTS (SELECT 1 FROM label_rejections r WHERE r.message_id = m.id AND r.label_id = ?)
				ON CONFLICT(message_id, label_id) DO NOTHING
				RETURNING message_id`, append(append([]any{labelID, now}, anySlice(chunk)...), labelID)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				added = append(added, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// RuleCandidate is what label rules look at.
type RuleCandidate struct {
	ID, AccountID, FromName, FromEmail, Subject string
	InSpam                                      bool
}

// ruleCandidatesQuery selects received mail (not sent, drafts or trash).
const ruleCandidatesQuery = `SELECT id, account_id, from_name, from_email, subject, (folder_kinds & ?) != 0 FROM messages
	WHERE (folder_kinds & ?) = 0 AND folder_kinds != 0`

func ruleCandidatesArgs(ids []string) []any {
	return append([]any{kindBits["spam"], kindMask([]string{"sent", "drafts", "trash"})}, anySlice(ids)...)
}

// RuleCandidates lists the given messages for label rules, skipping sent
// mail, drafts and trash. It looks them up by id, so rules on a big first
// sync don't scan the whole mailbox.
func (s *Store) RuleCandidates(ids []string) ([]RuleCandidate, error) {
	var out []RuleCandidate
	for _, chunk := range chunks(ids, 500) {
		var err error
		if out, err = s.scanRuleCandidates(out, ruleCandidatesQuery+` AND id IN (`+placeholders(len(chunk))+`)`, ruleCandidatesArgs(chunk)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AllRuleCandidates lists every cached received message ("Apply to
// existing mail").
func (s *Store) AllRuleCandidates() ([]RuleCandidate, error) {
	return s.scanRuleCandidates(nil, ruleCandidatesQuery, ruleCandidatesArgs(nil))
}

func (s *Store) scanRuleCandidates(out []RuleCandidate, query string, args []any) ([]RuleCandidate, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c RuleCandidate
		if err := rows.Scan(&c.ID, &c.AccountID, &c.FromName, &c.FromEmail, &c.Subject, &c.InSpam); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
