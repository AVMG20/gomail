package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// labelIndex lets search resolve "label:<name>" to label ids.
type labelIndex struct {
	mu     sync.RWMutex
	byName map[string]string
}

// SetLabels tells the store the current label definitions (used to resolve
// "label:" search operators by name or id).
func (s *Store) SetLabels(labels []models.Label) {
	m := map[string]string{}
	for _, l := range labels {
		m[strings.ToLower(l.ID)] = l.ID
		m[strings.ToLower(l.Name)] = l.ID
	}
	s.labels.mu.Lock()
	s.labels.byName = m
	s.labels.mu.Unlock()
}

func (s *Store) resolveLabel(name string) string {
	s.labels.mu.RLock()
	defer s.labels.mu.RUnlock()
	if id, ok := s.labels.byName[strings.ToLower(name)]; ok {
		return id
	}
	return strings.ToLower(name)
}

// where accumulates SQL conditions over the "m" (messages) alias.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	w.conds = append(w.conds, cond)
	w.args = append(w.args, args...)
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// Filter options beyond models.ListQuery used by bulk operations.
type FilterOptions struct {
	// ExcludeFolderKinds drops messages that are in any folder of these kinds.
	ExcludeFolderKinds []string
	// OnlyUnlabeled keeps messages without any label.
	OnlyUnlabeled bool
}

const folderKindExists = `EXISTS (SELECT 1 FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE mf.message_id = m.id AND f.kind IN (%s))`

// buildFilter translates a ListQuery (+ options) into SQL conditions.
func (s *Store) buildFilter(q models.ListQuery, opt FilterOptions) (*where, error) {
	w := &where{}
	if q.AccountID != "" {
		w.add(`m.account_id = ?`, q.AccountID)
	}
	if q.FolderID != "" {
		f, err := s.Folder(q.FolderID)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("folder no longer exists")
		}
		if err != nil {
			return nil, err
		}
		if f.Kind == models.FolderStarred {
			w.add(`m.account_id = ? AND m.starred = 1`, f.AccountID)
		} else {
			w.add(`EXISTS (SELECT 1 FROM message_folders mf WHERE mf.message_id = m.id AND mf.folder_id = ?)`, f.ID)
		}
	} else if q.FolderKind != "" {
		addKindFilter(w, []string{q.FolderKind})
	}
	if len(q.Labels) > 0 {
		w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (`+placeholders(len(q.Labels))+`))`, anySlice(q.Labels)...)
	}
	if q.Unlabeled || opt.OnlyUnlabeled {
		w.add(`NOT EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id)`)
	}
	if q.UnreadOnly {
		w.add(`m.unread = 1`)
	}
	if len(opt.ExcludeFolderKinds) > 0 {
		w.add(`NOT `+fmt.Sprintf(folderKindExists, placeholders(len(opt.ExcludeFolderKinds))), anySlice(opt.ExcludeFolderKinds)...)
	}
	if strings.TrimSpace(q.Search) != "" {
		ps := ParseSearch(q.Search)
		s.addSearchFilters(w, ps)
		if expr := ps.FTSQuery(); expr != "" {
			w.add(`m.rid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)`, expr)
		}
	}
	return w, nil
}

func addKindFilter(w *where, kinds []string) {
	var real []string
	starred := false
	for _, k := range kinds {
		if k == models.FolderStarred {
			starred = true
		} else {
			real = append(real, k)
		}
	}
	var parts []string
	var args []any
	if starred {
		parts = append(parts, `m.starred = 1`)
	}
	if len(real) > 0 {
		parts = append(parts, fmt.Sprintf(folderKindExists, placeholders(len(real))))
		args = append(args, anySlice(real)...)
	}
	w.add(`(`+strings.Join(parts, " OR ")+`)`, args...)
}

// addSearchFilters adds the non-FTS operators (is:, has:, in:, label:,
// before:, after:).
func (s *Store) addSearchFilters(w *where, ps ParsedSearch) {
	if ps.Unread != nil {
		w.add(`m.unread = ?`, boolInt(*ps.Unread))
	}
	if ps.Starred != nil {
		w.add(`m.starred = ?`, boolInt(*ps.Starred))
	}
	if ps.HasAttachment {
		w.add(`m.has_attachments = 1`)
	}
	if len(ps.In) > 0 {
		addKindFilter(w, ps.In)
	}
	if len(ps.Labels) > 0 {
		ids := make([]string, len(ps.Labels))
		for i, l := range ps.Labels {
			ids[i] = s.resolveLabel(l)
		}
		// Each label: operator must match (AND semantics, like Gmail).
		for _, id := range ids {
			w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id = ?)`, id)
		}
	}
	if !ps.After.IsZero() {
		w.add(`m.date >= ?`, ps.After.UnixMilli())
	}
	if !ps.Before.IsZero() {
		w.add(`m.date < ?`, ps.Before.UnixMilli())
	}
}

func encodeCursor(date int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(date, 10) + ":" + id))
}

func decodeCursor(c string) (int64, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", errors.New("invalid cursor")
	}
	ds, id, ok := strings.Cut(string(b), ":")
	if !ok {
		return 0, "", errors.New("invalid cursor")
	}
	d, err := strconv.ParseInt(ds, 10, 64)
	if err != nil {
		return 0, "", errors.New("invalid cursor")
	}
	return d, id, nil
}

// Page size limits for ListMessages.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// ListMessages returns one page of message summaries, newest first, using
// keyset pagination on (date, id).
func (s *Store) ListMessages(q models.ListQuery) (models.MessagePage, error) {
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
	if err := s.db.QueryRow(`SELECT count(*) FROM messages m`+w.sql(), w.args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if q.Cursor != "" {
		d, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		w.add(`(m.date < ? OR (m.date = ? AND m.id < ?))`, d, d, id)
	}
	rows, err := s.db.Query(`SELECT `+summaryCols+` FROM messages m`+w.sql()+` ORDER BY m.date DESC, m.id DESC LIMIT ?`,
		append(w.args, limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanSummary(rows)
		if err != nil {
			return page, err
		}
		page.Messages = append(page.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Messages) > limit {
		page.Messages = page.Messages[:limit]
		last := page.Messages[limit-1]
		page.NextCursor = encodeCursor(toMillis(last.Date), last.ID)
	}
	return page, nil
}

// ResolveIDs returns the ids of up to max messages matching q, newest first.
func (s *Store) ResolveIDs(q models.ListQuery, opt FilterOptions, max int) ([]string, error) {
	w, err := s.buildFilter(q, opt)
	if err != nil {
		return nil, err
	}
	if q.Cursor != "" {
		q.Cursor = "" // a selection covers the whole result, not one page
	}
	rows, err := s.db.Query(`SELECT m.id FROM messages m`+w.sql()+` ORDER BY m.date DESC, m.id DESC LIMIT ?`, append(w.args, max)...)
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

// EstimateInput counts messages matching q and the number of characters the
// classifier would send for them (body capped at bodyCap characters).
func (s *Store) EstimateInput(q models.ListQuery, opt FilterOptions, bodyCap int) (count int, chars int64, err error) {
	w, err := s.buildFilter(q, opt)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRow(`SELECT count(*), coalesce(sum(length(m.subject) + length(m.from_name) + length(m.from_email) + length(m.to_json) +
		CASE WHEN m.body_text IS NOT NULL AND length(m.body_text) > 0 THEN min(length(m.body_text), ?) ELSE length(m.snippet) END), 0)
		FROM messages m`+w.sql(), append([]any{bodyCap}, w.args...)...).Scan(&count, &chars)
	return count, chars, err
}

// now is replaceable in tests.
var now = time.Now
