package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// ListOptions control how ListMessages groups results.
type ListOptions struct {
	// Conversations returns one row per thread (its latest message
	// matching the query) with thread-level aggregates.
	Conversations bool
	// OlderCutoff is the sync depth (Settings.syncDepthDays): older mail is
	// only fetched on request, so it doesn't set HasMoreRemote. Zero = all.
	OlderCutoff time.Time
}

// ConversationPrefix marks conversation ids ("c:<anchor message id>").
const ConversationPrefix = "c:"

var aliasRe = regexp.MustCompile(`\bm\.`)

// latestInThread restricts w to the newest message (by date, id) of each
// thread among the messages matching w.
func latestInThread(w *where) {
	// Trashed messages form their own conversation (like sameThread), so a
	// newer trashed reply doesn't hide the live thread.
	inner := []string{"n.account_id = m.account_id", "n.thread_key = m.thread_key",
		threadSide("n") + " = " + threadSide("m"),
		"(n.date > m.date OR (n.date = m.date AND n.id > m.id))"}
	for _, c := range w.conds {
		inner = append(inner, aliasRe.ReplaceAllString(c, "n."))
	}
	args := append([]any(nil), w.args...)
	// INDEXED BY keeps the planner on the thread index (another index on
	// e.g. unread would make this quadratic).
	w.add(`NOT EXISTS (SELECT 1 FROM messages n INDEXED BY messages_thread_list WHERE `+strings.Join(inner, " AND ")+`)`, args...)
}

// ListMessages returns one page of message summaries (or conversations),
// newest first, using keyset pagination on (date, id).
func (s *Store) ListMessages(q models.ListQuery, opt ListOptions) (models.MessagePage, error) {
	page := models.MessagePage{Messages: []models.MessageSummary{}}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	w, err := s.buildFilter(q, FilterOptions{})
	if err != nil {
		return page, err
	}
	if page.Total, err = s.listTotal(q, opt, w); err != nil {
		return page, err
	}
	if page.Total == 0 {
		// Nothing as typed ("oddido"): show close matches ("odido").
		if sw, ok, err := s.similarFilter(q, FilterOptions{}); err != nil {
			return page, err
		} else if ok {
			if n, err := s.listTotal(q, opt, sw); err != nil {
				return page, err
			} else if n > 0 {
				w, page.Total, page.Similar = sw, n, true
			}
		}
	}
	if opt.Conversations {
		latestInThread(w)
	}
	if q.Cursor != "" {
		d, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		w.add(`(m.date < ? OR (m.date = ? AND m.id < ?))`, d, d, id)
	}
	rows, err := s.db.Query(`SELECT `+s.summaryCols()+` FROM messages m`+w.sql()+` ORDER BY m.date DESC, m.id DESC LIMIT ?`,
		append(w.args, limit+1)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		m, err := scanSummary(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Messages = append(page.Messages, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Messages) > limit {
		page.Messages = page.Messages[:limit]
		last := page.Messages[limit-1]
		page.NextCursor = encodeCursor(toMillis(last.Date), last.ID)
	}
	if opt.Conversations {
		if err := s.aggregateThreads(page.Messages); err != nil {
			return page, err
		}
	} else {
		for i := range page.Messages {
			page.Messages[i].ThreadCount = 1
			page.Messages[i].Participants = []string{displayName(page.Messages[i].From)}
		}
	}
	if page.NextCursor == "" {
		page.HasMoreRemote, page.OlderBeyondDepth = s.hasMoreRemote(q, opt.OlderCutoff)
	}
	return page, nil
}

// convCountExpr counts conversations: distinct (account, thread, trashed).
// It spells out thread_key (a virtual column) so the list indexes, which
// hold thread_id, cover the count.
const convCountExpr = `count(DISTINCT m.account_id || char(0) || CASE WHEN m.thread_id = '' THEN m.id ELSE m.thread_id END || char(0) || ((m.folder_kinds & 32) != 0))`

// listTotal is the total of a list view: messages, or in conversation view
// the number of distinct threads with a matching message (cheaper than
// counting latest-per-thread rows).
func (s *Store) listTotal(q models.ListQuery, opt ListOptions, w *where) (int, error) {
	if n, ok, err := s.fastConversationCount(q, opt); err != nil || ok {
		return n, err
	}
	expr := `count(*)`
	if opt.Conversations {
		expr = convCountExpr
	}
	return s.cachedCount(`SELECT `+expr+` FROM messages m`+w.sql(), w.args)
}

func displayName(a models.Address) string {
	if a.Name != "" {
		return a.Name
	}
	if i := strings.Index(a.Email, "@"); i > 0 {
		return a.Email[:i]
	}
	return a.Email
}

// threadSide splits a thread into live, trashed and folderless (being
// deleted) messages, which never group together.
func threadSide(alias string) string {
	return `(CASE WHEN ` + alias + `.folder_kinds = 0 THEN 2 WHEN (` + alias + `.folder_kinds & 32) != 0 THEN 1 ELSE 0 END)`
}

// sameThread selects thread siblings of the anchor ?1 (account, thread and
// trash-ness: trashed messages only group with trashed ones).
var sameThread = `t.account_id = x.account_id AND t.thread_key = x.thread_key
	AND ` + threadSide("t") + ` = ` + threadSide("x")

var sourceRank = map[string]int{"manual": 4, "provider": 3, "system": 2, "ai": 1}

// aggregateThreads fills thread-level fields of conversation rows.
func (s *Store) aggregateThreads(rows []models.MessageSummary) error {
	if len(rows) == 0 {
		return nil
	}
	msgs, err := s.db.Prepare(`SELECT t.unread, t.starred, t.has_attachments, t.from_name, t.from_email, a.email
		FROM messages x JOIN messages t ON ` + sameThread + ` JOIN accounts a ON a.id = t.account_id
		WHERE x.id = ? ORDER BY t.date, t.id`)
	if err != nil {
		return err
	}
	defer msgs.Close()
	labels, err := s.db.Prepare(`SELECT ml.label_id, ml.source, max(ml.confidence)
		FROM messages x JOIN messages t ON ` + sameThread + ` JOIN message_labels ml ON ml.message_id = t.id
		WHERE x.id = ?` + s.labelCond() + ` GROUP BY ml.label_id, ml.source`)
	if err != nil {
		return err
	}
	defer labels.Close()
	for i := range rows {
		r := &rows[i]
		r.ConversationID = ConversationPrefix + r.ID
		rs, err := msgs.Query(r.ID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		r.Participants = []string{}
		count := 0
		for rs.Next() {
			var unread, starred, att int
			var name, email, me string
			if err := rs.Scan(&unread, &starred, &att, &name, &email, &me); err != nil {
				rs.Close()
				return err
			}
			count++
			r.Unread = r.Unread || unread == 1
			r.Starred = r.Starred || starred == 1
			r.HasAttachments = r.HasAttachments || att == 1
			p := displayName(models.Address{Name: name, Email: email})
			if strings.EqualFold(email, me) {
				p = "me"
			}
			if !seen[p] && len(r.Participants) < 6 {
				seen[p] = true
				r.Participants = append(r.Participants, p)
			}
		}
		rs.Close()
		r.ThreadCount = max(count, 1)
		lr, err := labels.Query(r.ID)
		if err != nil {
			return err
		}
		best := map[string]models.MessageLabel{}
		var order []string
		for lr.Next() {
			var l models.MessageLabel
			if err := lr.Scan(&l.LabelID, &l.Source, &l.Confidence); err != nil {
				lr.Close()
				return err
			}
			cur, ok := best[l.LabelID]
			if !ok {
				order = append(order, l.LabelID)
			}
			if !ok || sourceRank[l.Source] > sourceRank[cur.Source] {
				best[l.LabelID] = l
			}
		}
		lr.Close()
		if len(order) > 0 || len(r.Labels) > 0 {
			ids, details := []string{}, []models.MessageLabel{}
			for _, id := range sortedStrings(order) {
				ids = append(ids, id)
				details = append(details, best[id])
			}
			r.Labels, r.LabelDetails = ids, details
		}
	}
	return nil
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// ExpandMode says how ExpandConversations widens conversation ids.
type ExpandMode int

const (
	// ExpandThread: every message of the thread (same trash-ness).
	ExpandThread ExpandMode = iota
	// ExpandFolder: thread messages sharing a folder with the anchor (Gmail's
	// All Mail is ignored unless it is the anchor's only folder). Used for
	// archive/trash/move so e.g. replies in Sent stay where they are.
	ExpandFolder
	// ExpandAnchor: only the anchor (latest) message.
	ExpandAnchor
)

// IsConversationID reports whether id is a conversation id.
func IsConversationID(id string) bool { return strings.HasPrefix(id, ConversationPrefix) }

// ExpandConversations replaces conversation ids by message ids. Plain
// message ids pass through. Order is preserved, duplicates removed.
func (s *Store) ExpandConversations(ids []string, mode ExpandMode) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	push := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range ids {
		if !IsConversationID(id) {
			push(id)
			continue
		}
		anchor := strings.TrimPrefix(id, ConversationPrefix)
		if mode == ExpandAnchor {
			push(anchor)
			continue
		}
		rows, err := s.db.Query(`SELECT t.id FROM messages x JOIN messages t ON `+sameThread+` WHERE x.id = ? ORDER BY t.date, t.id`, anchor)
		if err != nil {
			return nil, err
		}
		var thread []string
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err != nil {
				rows.Close()
				return nil, err
			}
			thread = append(thread, tid)
		}
		rows.Close()
		if len(thread) == 0 {
			continue // anchor vanished
		}
		if mode == ExpandThread {
			for _, t := range thread {
				push(t)
			}
			continue
		}
		folders, err := s.MessageFolders(thread)
		if err != nil {
			return nil, err
		}
		kinds, err := s.folderKinds(folders[anchor])
		if err != nil {
			return nil, err
		}
		scope := map[string]bool{}
		for _, f := range folders[anchor] {
			if kinds[f] != models.FolderArchive {
				scope[f] = true
			}
		}
		if len(scope) == 0 {
			for _, f := range folders[anchor] {
				scope[f] = true
			}
		}
		push(anchor)
		for _, t := range thread {
			for _, f := range folders[t] {
				if scope[f] {
					push(t)
					break
				}
			}
		}
	}
	return out, nil
}

func (s *Store) folderKinds(ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		var k string
		err := s.db.QueryRow(`SELECT kind FROM folders WHERE id = ?`, id).Scan(&k)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out[id] = k
	}
	return out, nil
}

// QueryFolders returns the folders a plain folder view covers (FolderID,
// or FolderKind across accounts). Views with search/label/unread filters
// or the starred view return nil.
func (s *Store) QueryFolders(q models.ListQuery) ([]models.Folder, error) {
	if q.Search != "" || len(q.Labels) > 0 || len(q.ExcludeLabels) > 0 || q.Unlabeled || q.UnreadOnly {
		return nil, nil
	}
	if q.FolderID != "" {
		f, err := s.Folder(q.FolderID)
		if err != nil || f.Kind == models.FolderStarred {
			return nil, nil
		}
		return []models.Folder{f}, nil
	}
	if q.FolderKind == "" || q.FolderKind == models.FolderStarred {
		return nil, nil
	}
	all, err := s.Folders(q.AccountID)
	if err != nil {
		return nil, err
	}
	var out []models.Folder
	for _, f := range all {
		if f.Kind == q.FolderKind {
			out = append(out, f)
		}
	}
	return out, nil
}

func olderDoneKey(folderID string) string { return "olderdone:" + folderID }

// LocalFolderCount counts cached messages in a folder.
func (s *Store) LocalFolderCount(folderID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM message_folders WHERE folder_id = ?`, folderID).Scan(&n)
	return n, err
}

// OldestInFolder returns the date of the oldest cached message in a folder.
func (s *Store) OldestInFolder(folderID string) (time.Time, error) {
	var ms sql.NullInt64
	err := s.db.QueryRow(`SELECT min(m.date) FROM message_folders mf JOIN messages m ON m.id = mf.message_id WHERE mf.folder_id = ?`, folderID).Scan(&ms)
	if err != nil || !ms.Valid {
		return time.Time{}, err
	}
	return fromMillis(ms.Int64), nil
}

func olderCursorKey(folderID string) string { return "oldercursor:" + folderID }

// OlderCursor returns the date below which older mail of a folder is
// fetched next: the recorded cursor, else the oldest cached message.
func (s *Store) OlderCursor(accountID, folderID string) (time.Time, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM sync_state WHERE account_id = ? AND key = ?`, accountID, olderCursorKey(folderID)).Scan(&v)
	if err == nil {
		if ms, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			return fromMillis(ms), nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	return s.OldestInFolder(folderID)
}

// AdvanceOlderCursor moves a folder's older-mail cursor down to the oldest
// of a fetched page of older mail.
func (s *Store) AdvanceOlderCursor(accountID, folderID string, page []time.Time) error {
	var oldest time.Time
	for _, d := range page {
		if oldest.IsZero() || d.Before(oldest) {
			oldest = d
		}
	}
	if oldest.IsZero() {
		return nil
	}
	cur, err := s.OlderCursor(accountID, folderID)
	if err != nil {
		return err
	}
	if !cur.IsZero() && !oldest.Before(cur) {
		if !oldest.Truncate(time.Second).Equal(cur.Truncate(time.Second)) {
			return nil // never moves up
		}
		// No progress: the page only held mail of the cursor's second
		// (ListOlder includes it). Step past that second, or paging would
		// fetch the same page forever.
		oldest = cur.Truncate(time.Second).Add(-time.Millisecond)
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		return saveSyncState(tx, accountID, map[string]string{olderCursorKey(folderID): strconv.FormatInt(toMillis(oldest), 10)})
	})
}

// SetOlderDone records that the server has no older mail for a folder.
func (s *Store) SetOlderDone(accountID, folderID string, done bool) error {
	v := ""
	if done {
		v = "1"
	}
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		return saveSyncState(tx, accountID, map[string]string{olderDoneKey(folderID): v})
	})
}

// hasMoreRemote reports whether the server has older mail for the view
// within the sync depth (more), and whether it has some only past it (beyond).
func (s *Store) hasMoreRemote(q models.ListQuery, cutoff time.Time) (more, beyond bool) {
	folders, err := s.QueryFolders(q)
	if err != nil {
		return false, false
	}
	for _, f := range folders {
		state, _ := s.SyncState(f.AccountID)
		if state[olderDoneKey(f.ID)] == "1" {
			continue
		}
		local, err := s.LocalFolderCount(f.ID)
		if err != nil || f.Total <= local {
			continue
		}
		if !cutoff.IsZero() {
			if oldest, err := s.OlderCursor(f.AccountID, f.ID); err == nil && !oldest.IsZero() && oldest.Before(cutoff) {
				beyond = true
				continue
			}
		}
		return true, false
	}
	return false, beyond
}

// fastConversationCount answers the conversation total of plain folder
// views from the trigger-maintained folder_threads counters.
func (s *Store) fastConversationCount(q models.ListQuery, opt ListOptions) (int, bool, error) {
	if !opt.Conversations || q.Search != "" || len(q.Labels) > 0 || len(q.ExcludeLabels) > 0 || q.Unlabeled {
		return 0, false, nil
	}
	var folders []string
	switch {
	case q.FolderID != "":
		f, err := s.Folder(q.FolderID)
		if err != nil || f.Kind == models.FolderStarred {
			return 0, false, nil
		}
		folders = []string{f.ID}
	case q.FolderKind != "" && q.FolderKind != models.FolderStarred && q.FolderKind != models.FolderCustom:
		all, err := s.Folders(q.AccountID)
		if err != nil {
			return 0, false, err
		}
		for _, f := range all {
			if f.Kind == q.FolderKind {
				folders = append(folders, f.ID)
			}
		}
		if len(folders) == 0 {
			return 0, true, nil
		}
	default:
		return 0, false, nil
	}
	query := `SELECT count(*) FROM folder_threads WHERE folder_id IN (` + placeholders(len(folders)) + `)`
	if q.UnreadOnly {
		query += ` AND unread > 0`
	}
	var n int
	err := s.db.QueryRow(query, anySlice(folders)...).Scan(&n)
	return n, err == nil, err
}

// slowConversationCount is the reference count (tests).
func (s *Store) slowConversationCount(q models.ListQuery) (int, error) {
	w, err := s.buildFilter(q, FilterOptions{})
	if err != nil {
		return 0, err
	}
	var n int
	err = s.db.QueryRow(`SELECT count(DISTINCT m.account_id || char(0) || m.thread_key) FROM messages m`+w.sql(), w.args...).Scan(&n)
	return n, err
}
