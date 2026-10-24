package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
)

// labelIndex lets search resolve "label:<name>" and "#<prefix>" to label
// ids. Every mailbox has its own labels, so a name can stand for several ids.
type labelIndex struct {
	mu     sync.RWMutex
	byName map[string][]string
	names  []labelName // for "#<prefix>"
	// imported is a SQL condition on the "ml" alias leaving out the
	// imported labels (Gmail labels, Outlook categories), whatever put
	// them on the mail.
	imported string
}

type labelName struct{ name, id string }

// SetLabels tells the store the current label definitions (used to resolve
// "label:" and "#" search operators by name or id).
func (s *Store) SetLabels(labels []models.Label) {
	m := map[string][]string{}
	names := make([]labelName, 0, len(labels))
	var imported []string
	for _, l := range labels {
		names = append(names, labelName{strings.ToLower(l.Name), l.ID})
		if l.Source == config.LabelSourceProvider {
			imported = append(imported, "'"+strings.ReplaceAll(l.ID, "'", "''")+"'")
		}
		for _, k := range []string{strings.ToLower(l.ID), strings.ToLower(l.Name)} {
			if !slices.Contains(m[k], l.ID) {
				m[k] = append(m[k], l.ID)
			}
		}
	}
	s.labels.mu.Lock()
	s.labels.byName = m
	s.labels.names = names
	s.labels.imported = ""
	if len(imported) > 0 {
		s.labels.imported = ` AND ml.label_id NOT IN (` + strings.Join(imported, ",") + `)`
	}
	s.labels.mu.Unlock()
}

// resolveLabels returns the ids of the labels called name (or with that
// id), in any mailbox.
func (s *Store) resolveLabels(name string) []string {
	s.labels.mu.RLock()
	defer s.labels.mu.RUnlock()
	if ids, ok := s.labels.byName[strings.ToLower(name)]; ok {
		return ids
	}
	return []string{strings.ToLower(name)}
}

// resolveLabelPrefix returns the ids of the labels whose name starts with
// prefix, in any mailbox.
func (s *Store) resolveLabelPrefix(prefix string) []string {
	prefix = strings.ToLower(prefix)
	s.labels.mu.RLock()
	defer s.labels.mu.RUnlock()
	var ids []string
	for _, l := range s.labels.names {
		if strings.HasPrefix(l.name, prefix) {
			ids = append(ids, l.id)
		}
	}
	return ids
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
	// FTSExpr replaces the search's full-text query (the spell-corrected
	// one, see similarFilter).
	FTSExpr string
}

// visible is the SQL condition for messages shown anywhere: a message in
// no folder (e.g. one being deleted by emptying Trash) is hidden until it
// is gone or comes back.
func visible(alias string) string { return alias + `.folder_kinds != 0` }

// allMailCond is Outlook's All Mail: Outlook has no such folder (its
// "All Mail" is the Archive folder), so gomail shows every message of the
// mailbox except Junk and Trash, like Gmail's.
const allMailCond = `(m.folder_kinds != 0 AND (m.folder_kinds & 48) = 0)`

// outlookAccounts are the ids of the Outlook mailboxes.
func (s *Store) outlookAccounts() []string {
	rows, err := s.db.Query(`SELECT id FROM accounts WHERE provider = ?`, models.ProviderOutlook)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// isAllMail reports whether f is an Outlook mailbox's All Mail.
func (s *Store) isAllMail(f models.Folder) bool {
	return f.Kind == models.FolderArchive && slices.Contains(s.outlookAccounts(), f.AccountID)
}

// buildFilter translates a ListQuery (+ options) into SQL conditions.
func (s *Store) buildFilter(q models.ListQuery, opt FilterOptions) (*where, error) {
	w := &where{}
	inFolder := false // the folder condition already implies visible()
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
			inFolder = true
			w.add(`m.account_id = ? AND (m.folder_kinds & 64) != 0`, f.AccountID)
			w.add(`EXISTS (SELECT 1 FROM message_folders mf WHERE mf.message_id = m.id AND mf.folder_id = ?)`, f.ID)
		case models.FolderArchive:
			inFolder = true
			if s.isAllMail(f) {
				w.add(`m.account_id = ? AND `+allMailCond, f.AccountID)
			} else {
				w.add(`m.account_id = ? AND (m.folder_kinds & ?) != 0`, f.AccountID, kindBits[f.Kind])
			}
		default:
			// One system folder per kind and account: the bitmask is exact.
			inFolder = true
			w.add(`m.account_id = ? AND (m.folder_kinds & ?) != 0`, f.AccountID, kindBits[f.Kind])
		}
	} else if q.FolderKind != "" {
		s.addKindFilter(w, []string{q.FolderKind})
	}
	if !inFolder {
		w.add(visible("m"))
	}
	// The label filter: any of the chosen labels, or "No label" (q.Unlabeled), OR-combined
	// like the filter popover. OnlyUnlabeled (bulk AI labeling) always restricts.
	hasLabel := `EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (` + placeholders(len(q.Labels)) + `)` + s.labelCond() + `)`
	if len(q.Labels) > 0 && !q.Unlabeled {
		few, err := s.fewLabeled(q.Labels)
		if err != nil {
			return nil, err
		}
		if few {
			// A rare label: look its messages up. Walking the view in date
			// order and checking each message reads all of it to count, or
			// to fill a page the label is rare in (all mailboxes: 55 ms ->
			// 3 ms).
			hasLabel = `m.id IN (SELECT ml.message_id FROM message_labels ml WHERE ml.label_id IN (` + placeholders(len(q.Labels)) + `)` + s.labelCond() + `)`
		}
	}
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
		expr := ps.FTSQuery()
		if opt.FTSExpr != "" {
			expr = opt.FTSExpr
		}
		if expr != "" {
			// "+" keeps the planner from fetching every match by rowid (each
			// a row read, past cached bodies for folder_kinds): it walks a
			// covering list index in date order and checks the matches
			// instead, which bounds common words to one index scan.
			w.add(`+m.rid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)`, expr)
		}
	}
	return w, nil
}

// fewLabels is the most label assignments a label filter looks up message
// by message (a row read each, ~6 µs); past it, walking the view in date
// order wins for pages and for views narrowed otherwise (unread). A var
// for tests.
var fewLabels = 1000

// fewLabeled reports whether the labels are on at most fewLabels messages.
func (s *Store) fewLabeled(labels []string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM message_labels WHERE label_id IN (`+placeholders(len(labels))+`) LIMIT ?)`,
		append(anySlice(labels), fewLabels+1)...).Scan(&n)
	return n <= fewLabels, err
}

func (s *Store) addKindFilter(w *where, kinds []string) {
	var real []string
	starred, archive := false, false
	for _, k := range kinds {
		if k == models.FolderStarred {
			starred = true
		} else {
			real = append(real, k)
			archive = archive || k == models.FolderArchive
		}
	}
	var parts []string
	var args []any
	if starred {
		parts = append(parts, `m.starred = 1`)
	}
	if outlook := s.outlookAccounts(); archive && len(outlook) > 0 {
		parts = append(parts, `(m.account_id IN (`+placeholders(len(outlook))+`) AND `+allMailCond+`)`)
		args = append(args, anySlice(outlook)...)
	}
	if len(real) > 0 {
		parts = append(parts, `(m.folder_kinds & ?) != 0`)
		args = append(args, kindMask(real))
	}
	w.add(`(`+strings.Join(parts, " OR ")+`)`, args...)
}

// addSearchFilters adds the non-FTS operators (is:, has:, in:, label:,
// #label, before:, after:).
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
		s.addKindFilter(w, ps.In)
	}
	if len(ps.Labels) > 0 {
		// Each label: operator must match (AND semantics, like Gmail); a
		// name matches that label in every mailbox.
		for _, l := range ps.Labels {
			ids := s.resolveLabels(l)
			w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (`+placeholders(len(ids))+`)`+s.labelCond()+`)`, anySlice(ids)...)
		}
	}
	// Each #prefix: a label whose name starts with it (no such label: no mail).
	for _, p := range ps.LabelPrefixes {
		ids := s.resolveLabelPrefix(p)
		if len(ids) == 0 {
			w.add(`0`)
			continue
		}
		w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (`+placeholders(len(ids))+`)`+s.labelCond()+`)`, anySlice(ids)...)
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

// similarFilter is the filter for q with its search words spell-corrected,
// for when the search as typed matches nothing (ok false: nothing to
// correct). The list and "select all" then show the same close matches as
// the search box.
func (s *Store) similarFilter(q models.ListQuery, opt FilterOptions) (*where, bool, error) {
	if strings.TrimSpace(q.Search) == "" {
		return nil, false, nil
	}
	expr, _, err := s.fuzzyExpr(ParseSearch(q.Search))
	if err != nil || expr == "" {
		return nil, false, err
	}
	opt.FTSExpr = expr
	w, err := s.buildFilter(q, opt)
	return w, err == nil, err
}

// ResolveIDs returns the ids of up to max messages matching q, newest first
// (close matches when the search as typed matches nothing, like the list).
func (s *Store) ResolveIDs(q models.ListQuery, opt FilterOptions, max int) ([]string, error) {
	ids, err := s.resolveIDs(q, opt, max, nil)
	if err != nil || len(ids) > 0 {
		return ids, err
	}
	w, ok, err := s.similarFilter(q, opt)
	if err != nil || !ok {
		return ids, err
	}
	return s.resolveIDs(q, opt, max, w)
}

func (s *Store) resolveIDs(q models.ListQuery, opt FilterOptions, max int, w *where) ([]string, error) {
	if w == nil {
		var err error
		if w, err = s.buildFilter(q, opt); err != nil {
			return nil, err
		}
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
// hash starting with their account's prefix in unchanged (would be
// skipped), and the characters the classifier would send for the others
// (body capped at bodyCap). Each mailbox has its own label set, so its own
// prefix; accounts missing from unchanged count as changed.
func (s *Store) EstimateInput(q models.ListQuery, opt FilterOptions, bodyCap int, unchanged map[string]string) (count, same int, chars int64, err error) {
	w, err := s.buildFilter(q, opt)
	if err != nil {
		return 0, 0, 0, err
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	accts := make([]string, 0, len(unchanged))
	for a := range unchanged {
		accts = append(accts, a)
	}
	sort.Strings(accts)
	cond, condArgs := "0", []any{}
	if len(accts) > 0 {
		var b strings.Builder
		b.WriteString("CASE m.account_id")
		for _, a := range accts {
			b.WriteString(` WHEN ? THEN m.ai_hash LIKE ? ESCAPE '\'`)
			condArgs = append(condArgs, a, esc.Replace(unchanged[a])+"%")
		}
		b.WriteString(" ELSE 0 END")
		cond = b.String()
	}
	args := append(append(append([]any{}, condArgs...), condArgs...), bodyCap)
	err = s.db.QueryRow(`SELECT count(*), coalesce(sum(CASE WHEN `+cond+` THEN 1 ELSE 0 END), 0),
		coalesce(sum(CASE WHEN `+cond+` THEN 0 ELSE length(m.subject) + length(m.from_name) + length(m.from_email) + length(m.to_json) +
		CASE WHEN m.body_text IS NOT NULL AND length(m.body_text) > 0 THEN min(length(m.body_text), ?) ELSE length(m.snippet) END END), 0)
		FROM messages m`+w.sql(), append(args, w.args...)...).Scan(&count, &same, &chars)
	return count, same, chars, err
}

// now is replaceable in tests.
var now = time.Now
