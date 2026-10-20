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
		switch f.Kind {
		case models.FolderStarred:
			w.add(`m.account_id = ? AND m.starred = 1`, f.AccountID)
		case models.FolderCustom:
			w.add(`m.account_id = ? AND (m.folder_kinds & 64) != 0`, f.AccountID)
			w.add(`EXISTS (SELECT 1 FROM message_folders mf WHERE mf.message_id = m.id AND mf.folder_id = ?)`, f.ID)
		default:
			// One system folder per kind and account: the bitmask is exact.
			w.add(`m.account_id = ? AND (m.folder_kinds & ?) != 0`, f.AccountID, kindBits[f.Kind])
		}
	} else if q.FolderKind != "" {
		addKindFilter(w, []string{q.FolderKind})
	}
	// The label filter: any of the chosen labels, or "No label" (q.Unlabeled), OR-combined
	// like the filter popover. OnlyUnlabeled (bulk AI labeling) always restricts.
	hasLabel := `EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (` + placeholders(len(q.Labels)) + `)` + s.labelCond() + `)`
	noLabel := `NOT EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id` + s.labelCond() + `)`
	switch {
	case len(q.Labels) > 0 && q.Unlabeled:
		w.add(`(`+hasLabel+` OR `+noLabel+`)`, anySlice(q.Labels)...)
	case len(q.Labels) > 0:
		w.add(hasLabel, anySlice(q.Labels)...)
	case q.Unlabeled:
		w.add(noLabel)
	}
	if len(q.ExcludeLabels) > 0 {
		w.add(`NOT EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (`+placeholders(len(q.ExcludeLabels))+`)`+s.labelCond()+`)`, anySlice(q.ExcludeLabels)...)
	}
	if opt.OnlyUnlabeled {
		w.add(noLabel)
	}
	if q.UnreadOnly {
		w.add(`m.unread = 1`)
	}
	if len(opt.ExcludeFolderKinds) > 0 {
		w.add(`(m.folder_kinds & ?) = 0`, kindMask(opt.ExcludeFolderKinds))
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
		parts = append(parts, `(m.folder_kinds & ?) != 0`)
		args = append(args, kindMask(real))
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
			w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id = ?`+s.labelCond()+`)`, id)
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

// EstimateInput counts messages matching q, how many already carry an AI
// hash starting with unchangedPrefix (would be skipped), and the characters
// the classifier would send for the others (body capped at bodyCap).
func (s *Store) EstimateInput(q models.ListQuery, opt FilterOptions, bodyCap int, unchangedPrefix string) (count, unchanged int, chars int64, err error) {
	w, err := s.buildFilter(q, opt)
	if err != nil {
		return 0, 0, 0, err
	}
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(unchangedPrefix) + "%"
	err = s.db.QueryRow(`SELECT count(*), coalesce(sum(CASE WHEN m.ai_hash LIKE ? ESCAPE '\' THEN 1 ELSE 0 END), 0),
		coalesce(sum(CASE WHEN m.ai_hash LIKE ? ESCAPE '\' THEN 0 ELSE length(m.subject) + length(m.from_name) + length(m.from_email) + length(m.to_json) +
		CASE WHEN m.body_text IS NOT NULL AND length(m.body_text) > 0 THEN min(length(m.body_text), ?) ELSE length(m.snippet) END END), 0)
		FROM messages m`+w.sql(), append([]any{like, like, bodyCap}, w.args...)...).Scan(&count, &unchanged, &chars)
	return count, unchanged, chars, err
}

// now is replaceable in tests.
var now = time.Now
