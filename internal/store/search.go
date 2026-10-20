package store

import (
	"database/sql"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

// ParsedSearch is a parsed search string.
type ParsedSearch struct {
	Terms         []SearchTerm // free text
	From, To      []string
	Subject       []string
	Labels        []string
	In            []string // folder kinds
	Unread        *bool
	Starred       *bool
	HasAttachment bool
	Before, After time.Time
	// PrefixLast is true when the last free term should prefix-match
	// (search-as-you-type: the query doesn't end with whitespace).
	PrefixLast bool
}

// SearchTerm is a free-text word or "quoted phrase".
type SearchTerm struct {
	Text   string
	Phrase bool
}

var folderKindAliases = map[string]string{
	"inbox": models.FolderInbox, "starred": models.FolderStarred, "flagged": models.FolderStarred,
	"sent": models.FolderSent, "drafts": models.FolderDrafts, "draft": models.FolderDrafts,
	"archive": models.FolderArchive, "all": models.FolderArchive, "spam": models.FolderSpam, "junk": models.FolderSpam,
	"trash": models.FolderTrash, "bin": models.FolderTrash, "deleted": models.FolderTrash,
}

// splitQuery splits on whitespace while keeping "quoted strings" (also in
// key:"value" form) together. Quotes are preserved in the output tokens.
func splitQuery(q string) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case unicode.IsSpace(r) && !inQuote:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func unquote(s string) (string, bool) {
	if strings.HasPrefix(s, `"`) {
		s = strings.TrimPrefix(s, `"`)
		s = strings.TrimSuffix(s, `"`)
		return strings.ReplaceAll(s, `"`, ""), true
	}
	return strings.ReplaceAll(s, `"`, ""), false
}

func parseDate(s string) time.Time {
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006-1-2", "2006/1/2"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ParseSearch parses a search string with operators.
func ParseSearch(q string) ParsedSearch {
	var ps ParsedSearch
	t, f := true, false
	toks := splitQuery(q)
	lastFree := -1
	for _, tok := range toks {
		key, val, hasKey := strings.Cut(tok, ":")
		if hasKey && !strings.HasPrefix(tok, `"`) && val != "" {
			v, _ := unquote(val)
			lv := strings.ToLower(v)
			handled := true
			switch strings.ToLower(key) {
			case "from":
				ps.From = append(ps.From, v)
			case "to", "cc":
				ps.To = append(ps.To, v)
			case "subject":
				ps.Subject = append(ps.Subject, v)
			case "label", "tag":
				ps.Labels = append(ps.Labels, v)
			case "in":
				if k, ok := folderKindAliases[lv]; ok {
					ps.In = append(ps.In, k)
				}
			case "is":
				switch lv {
				case "unread":
					ps.Unread = &t
				case "read":
					ps.Unread = &f
				case "starred", "flagged":
					ps.Starred = &t
				case "unstarred":
					ps.Starred = &f
				}
			case "has":
				if strings.HasPrefix(lv, "attachment") {
					ps.HasAttachment = true
				}
			case "before":
				ps.Before = parseDate(v)
			case "after", "since":
				ps.After = parseDate(v)
			default:
				handled = false
			}
			if handled {
				continue
			}
		}
		text, phrase := unquote(tok)
		if !hasWordChar(text) {
			continue
		}
		ps.Terms = append(ps.Terms, SearchTerm{Text: text, Phrase: phrase})
		lastFree = len(ps.Terms) - 1
	}
	ps.PrefixLast = lastFree >= 0 && !ps.Terms[lastFree].Phrase &&
		len(q) > 0 && !unicode.IsSpace(rune(q[len(q)-1]))
	return ps
}

func hasWordChar(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// ftsString quotes s as an FTS5 string, so no user input is ever parsed as
// FTS syntax.
func ftsString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// FTSQuery builds a safe FTS5 MATCH expression, or "" when the search has
// no full-text part.
func (ps ParsedSearch) FTSQuery() string {
	var parts []string
	for i, t := range ps.Terms {
		p := ftsString(t.Text)
		// A one-letter prefix would match nearly everything.
		if i == len(ps.Terms)-1 && ps.PrefixLast && len([]rune(t.Text)) >= 2 {
			p += "*"
		}
		parts = append(parts, p)
	}
	col := func(c string, vals []string) {
		for _, v := range vals {
			if hasWordChar(v) {
				parts = append(parts, c+" : "+ftsString(v)+"*")
			}
		}
	}
	col("sender", ps.From)
	col("recipients", ps.To)
	col("subject", ps.Subject)
	return strings.Join(parts, " AND ")
}

// highlightTerms are the words to mark in excerpts.
func (ps ParsedSearch) highlightTerms() []hlTerm {
	var out []hlTerm
	for i, t := range ps.Terms {
		out = append(out, hlTerm{text: t.Text, prefix: !t.Phrase && (i < len(ps.Terms)-1 || ps.PrefixLast)})
	}
	for _, v := range append(append(append([]string{}, ps.From...), ps.To...), ps.Subject...) {
		out = append(out, hlTerm{text: v, prefix: true})
	}
	return out
}

// Search limits.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 100
)

// candidate is a search hit before highlighting.
type candidate struct {
	sum   models.MessageSummary
	score float64
	fuzzy bool
	terms []hlTerm
}

// Search ranks messages across accounts for the command palette: an FTS5
// match (prefix on the last word) over subject, sender, recipients, snippet
// and cached body text, scored by field weights (subject > from > to >
// snippet > body) times a recency boost. When there are few hits, query
// words are spell-corrected against the vocabulary of subjects and senders
// (edit distance) and searched again (Fuzzy results).
func (s *Store) Search(q models.SearchQuery) ([]models.SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	ps := ParseSearch(q.Query)
	w := &where{}
	if q.AccountID != "" {
		w.add(`m.account_id = ?`, q.AccountID)
	}
	if len(q.Labels) > 0 {
		w.add(`EXISTS (SELECT 1 FROM message_labels ml WHERE ml.message_id = m.id AND ml.label_id IN (`+placeholders(len(q.Labels))+`)`+s.labelCond()+`)`, anySlice(q.Labels)...)
	}
	s.addSearchFilters(w, ps)
	nowMs := now().UnixMilli()
	recency := fmt.Sprintf(`(1.0 + 1.0 / (1.0 + max(0, %d - m.date) / 2592000000.0))`, nowMs) // == recencyBoost

	var cands []candidate
	seen := map[string]bool{}
	expr := ps.FTSQuery()
	if expr == "" && len(w.conds) == 0 {
		return []models.SearchResult{}, nil
	}
	w.add(visible("m"))
	if expr != "" {
		var err error
		cands, err = s.ftsCandidates(expr, w, ps.highlightTerms(), nowMs)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
		if len(cands) > limit {
			cands = cands[:limit]
		}
		for _, c := range cands {
			seen[c.sum.ID] = true
		}
		if len(cands) < limit/2+1 && len(ps.Terms) > 0 {
			fz, err := s.fuzzySearch(ps, w, nowMs, seen)
			if err != nil {
				return nil, err
			}
			sort.SliceStable(fz, func(i, j int) bool { return fz[i].score > fz[j].score })
			cands = append(cands, fz...)
		}
	} else {
		rows, err := s.db.Query(`SELECT `+s.summaryCols()+`, `+recency+` FROM messages m`+w.sql()+` ORDER BY m.date DESC, m.id DESC LIMIT ?`,
			append(w.args, limit)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c candidate
			sum, err := scanSummary(rows, &c.score)
			if err != nil {
				rows.Close()
				return nil, err
			}
			c.sum = sum
			cands = append(cands, c)
		}
		rows.Close()
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > limit {
		cands = cands[:limit]
	}
	if err := s.fillSummaries(cands); err != nil {
		return nil, err
	}
	out := make([]models.SearchResult, 0, len(cands))
	for _, c := range cands {
		r := models.SearchResult{Message: c.sum, Score: c.score, Fuzzy: c.fuzzy}
		r.Highlight, r.MatchedIn = s.excerpt(c.sum, c.terms)
		out = append(out, r)
	}
	return out, nil
}

// Field weights for ranking: a term found in the subject counts most.
const (
	weightSubject = 10.0
	weightFrom    = 6.0
	weightTo      = 3.0
	weightSnippet = 2.0
	weightOther   = 1.0 // body (or tokenizer-only match)
	ftsPool       = 600 // most recent full-text hits considered for ranking
)

// ftsCandidates scores the most recent full-text hits. Ranking is done in
// Go (field weights x recency) rather than with bm25(), which costs ~10x
// more than the match itself on large mailboxes.
func (s *Store) ftsCandidates(expr string, w *where, terms []hlTerm, nowMs int64) ([]candidate, error) {
	filter := ""
	if len(w.conds) > 0 {
		filter = " AND " + strings.Join(w.conds, " AND ")
	}
	rows, err := s.db.Query(`SELECT m.id, m.date, m.subject, m.from_name, m.from_email, m.to_json, m.snippet
		FROM messages m WHERE m.rid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)`+filter+`
		ORDER BY m.date DESC LIMIT `+fmt.Sprint(ftsPool), append([]any{expr}, w.args...)...)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var id, subject, fromName, fromEmail, toJSON, snippet string
		var date int64
		if err := rows.Scan(&id, &date, &subject, &fromName, &fromEmail, &toJSON, &snippet); err != nil {
			return nil, err
		}
		total := 0.0
		for _, t := range terms {
			switch {
			case matchTerm(subject, t):
				total += weightSubject
			case matchTerm(fromName+" "+fromEmail, t):
				total += weightFrom
			case matchTerm(toJSON, t):
				total += weightTo
			case matchTerm(snippet, t):
				total += weightSnippet
			default:
				total += weightOther
			}
		}
		if len(terms) > 0 {
			total /= float64(len(terms))
		}
		out = append(out, candidate{sum: models.MessageSummary{ID: id}, score: total * recencyBoost(date, nowMs), terms: terms})
	}
	return out, rows.Err()
}

// recencyBoost is a multiplier in (1, 2]: 2 for brand new mail, 1.5 after
// 30 days, approaching 1 for old mail.
func recencyBoost(dateMs, nowMs int64) float64 {
	age := float64(max(0, nowMs-dateMs)) / 86400000.0
	return 1 + 1/(1+age/30)
}

// fillSummaries loads full summaries for candidates that only carry an id.
func (s *Store) fillSummaries(cands []candidate) error {
	var ids []string
	for _, c := range cands {
		if c.sum.AccountID == "" {
			ids = append(ids, c.sum.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sums, err := s.Summaries(ids)
	if err != nil {
		return err
	}
	byID := map[string]models.MessageSummary{}
	for _, m := range sums {
		byID[m.ID] = m
	}
	for i := range cands {
		if cands[i].sum.AccountID == "" {
			cands[i].sum = byID[cands[i].sum.ID]
		}
	}
	return nil
}

// fuzzySearch spell-corrects the free-text words against the vocabulary
// and runs the corrected full-text query.
func (s *Store) fuzzySearch(ps ParsedSearch, w *where, nowMs int64, seen map[string]bool) ([]candidate, error) {
	vocab, err := s.vocabulary()
	if err != nil {
		return nil, err
	}
	var groups []string
	var terms []hlTerm
	corrected := false
	for i, t := range ps.Terms {
		prefix := i == len(ps.Terms)-1 && ps.PrefixLast
		for _, word := range words(t.Text) {
			alts := vocab.similar(word, prefix)
			if len(word) < 3 || len(alts) == 0 {
				// Keep short / unknown words as typed.
				q := ftsString(string(word))
				if prefix && len(word) >= 2 {
					q += "*"
				}
				groups = append(groups, q)
				terms = append(terms, hlTerm{text: string(word), prefix: prefix})
				continue
			}
			corrected = true
			var ors []string
			for _, a := range alts {
				ors = append(ors, ftsString(a))
				terms = append(terms, hlTerm{text: a})
			}
			groups = append(groups, "("+strings.Join(ors, " OR ")+")")
		}
	}
	if !corrected {
		return nil, nil
	}
	// Operators other than free text still apply.
	col := func(c string, vals []string) {
		for _, v := range vals {
			if hasWordChar(v) {
				groups = append(groups, c+" : "+ftsString(v)+"*")
			}
		}
	}
	col("sender", ps.From)
	col("recipients", ps.To)
	col("subject", ps.Subject)
	cands, err := s.ftsCandidates(strings.Join(groups, " AND "), w, terms, nowMs)
	if err != nil {
		return nil, err
	}
	out := cands[:0]
	for _, c := range cands {
		if seen[c.sum.ID] {
			continue
		}
		c.fuzzy = true
		c.score *= 0.6 // rank below exact matches of similar age
		out = append(out, c)
	}
	return out, nil
}

// vocab is the in-memory set of words seen in subjects and senders, bucketed
// by length, used for spelling correction.
type vocab struct {
	mu    sync.RWMutex
	byLen map[int][][]rune
	seen  map[string]bool
}

func (v *vocab) add(w string) {
	if v.seen[w] {
		return
	}
	v.seen[w] = true
	r := []rune(w)
	v.byLen[len(r)] = append(v.byLen[len(r)], r)
}

// similar returns up to 8 vocabulary words close to w (and, for prefix
// matching, words whose beginning is close to w), best first. w itself is
// included when it is a known word.
func (v *vocab) similar(w []rune, prefix bool) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	type hit struct {
		word string
		d    int
	}
	var hits []hit
	allowed := 1
	if len(w) >= 7 {
		allowed = 2
	}
	maxLen := len(w) + allowed
	if prefix {
		maxLen = len(w) + 30
	}
	for l := max(1, len(w)-allowed); l <= maxLen; l++ {
		for _, cand := range v.byLen[l] {
			// Cheap filter: typos rarely change both of the first two letters.
			if cand[0] != w[0] && (len(cand) < 2 || len(w) < 2 || cand[1] != w[1]) {
				continue
			}
			d := osaDistance(w, cand)
			if prefix && len(cand) > len(w) {
				d = min(d, osaDistance(w, cand[:len(w)]), osaDistance(w, cand[:len(w)+1]))
			}
			if d <= allowed {
				hits = append(hits, hit{string(cand), d})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return len(hits[i].word) < len(hits[j].word)
	})
	if len(hits) > 8 {
		hits = hits[:8]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.word
	}
	return out
}

// vocabulary loads the vocabulary on first use.
func (s *Store) vocabulary() (*vocab, error) {
	s.vocabMu.Lock()
	defer s.vocabMu.Unlock()
	if s.vocab != nil {
		return s.vocab, nil
	}
	v := &vocab{byLen: map[int][][]rune{}, seen: map[string]bool{}}
	rows, err := s.db.Query(`SELECT term FROM vocab`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		v.add(t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.vocab = v
	return v, nil
}

// vocabWords returns the folded words of a message worth remembering for
// spelling correction (subject and sender).
func vocabWords(m mail.Message) []string {
	var out []string
	for _, w := range words(m.Subject + " " + m.From.Name + " " + m.From.Email) {
		if len(w) >= 3 && len(w) <= 30 && !allDigits(w) {
			out = append(out, string(w))
		}
	}
	return out
}

func allDigits(r []rune) bool {
	for _, c := range r {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// wordSimilarity returns a score in (0, 1] when the (folded) query word is
// within the allowed edit distance of the haystack word or of its prefix of
// similar length, or 0.
func wordSimilarity(q, w []rune) float64 {
	allowed := 1
	if len(q) >= 7 {
		allowed = 2
	}
	best := -1
	if d := osaDistance(q, w); d <= allowed {
		best = d
	}
	// Search-as-you-type: compare against the word's prefix too.
	for _, n := range []int{len(q), len(q) + 1} {
		if n < len(w) {
			if d := osaDistance(q, w[:n]); d <= allowed && (best < 0 || d < best) {
				best = d
			}
		}
	}
	if best < 0 {
		return 0
	}
	return 1 - float64(best)/float64(len(q)+1)
}

// osaDistance is the optimal string alignment (Damerau-Levenshtein with
// adjacent transpositions) distance.
func osaDistance(a, b []rune) int {
	n, m := len(a), len(b)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}
	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[n][m]
}

// fold lower-cases a rune and strips diacritics (é -> e).
func fold(r rune) rune {
	if r < 128 {
		return unicode.ToLower(r)
	}
	d := norm.NFD.String(string(r))
	for _, x := range d {
		return unicode.ToLower(x)
	}
	return unicode.ToLower(r)
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// words splits s into folded words.
func words(s string) [][]rune {
	var out [][]rune
	var cur []rune
	for _, r := range s {
		if isWordRune(r) {
			cur = append(cur, fold(r))
		} else if len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// hlTerm is a word or phrase to highlight.
type hlTerm struct {
	text   string
	prefix bool
}

// excerpt picks the best field for the result line and highlights matches.
func (s *Store) excerpt(m models.MessageSummary, terms []hlTerm) (string, string) {
	if len(terms) == 0 {
		return html.EscapeString(truncateRunes(m.Snippet, 160)), ""
	}
	to := make([]string, 0, len(m.To))
	for _, a := range m.To {
		to = append(to, strings.TrimSpace(a.Name+" "+a.Email))
	}
	fields := []struct{ name, text string }{
		{"subject", m.Subject},
		{"from", strings.TrimSpace(m.From.Name + " <" + m.From.Email + ">")},
		{"to", strings.Join(to, ", ")},
		{"snippet", m.Snippet},
	}
	for _, f := range fields {
		if h, ok := Highlight(f.text, terms, 160); ok {
			return h, f.name
		}
	}
	var body sql.NullString
	if err := s.db.QueryRow(`SELECT body_text FROM messages WHERE id = ?`, m.ID).Scan(&body); err == nil && body.Valid {
		if h, ok := Highlight(body.String, terms, 160); ok {
			return h, "body"
		}
	}
	return html.EscapeString(truncateRunes(m.Snippet, 160)), ""
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// Highlight HTML-escapes text and wraps matches of terms in <mark></mark>.
// Matching is case- and diacritics-insensitive and anchored at word starts.
// The excerpt is cut to about maxRunes around the first match. ok is false
// when nothing matched.
type span struct{ a, b int }

func foldRunes(text string) (src, folded []rune) {
	src = []rune(strings.Join(strings.Fields(text), " "))
	folded = make([]rune, len(src))
	for i, r := range src {
		folded[i] = fold(r)
	}
	return src, folded
}

// findSpans locates t in folded text at word starts.
func findSpans(folded []rune, t hlTerm, first bool) []span {
	tr := []rune(strings.Join(strings.Fields(t.text), " "))
	for i := range tr {
		tr[i] = fold(tr[i])
	}
	// Trim non-word characters at the ends (e.g. "alice@" -> "alice").
	for len(tr) > 0 && !isWordRune(tr[0]) {
		tr = tr[1:]
	}
	for len(tr) > 0 && !isWordRune(tr[len(tr)-1]) {
		tr = tr[:len(tr)-1]
	}
	if len(tr) == 0 {
		return nil
	}
	var spans []span
	for i := 0; i+len(tr) <= len(folded); i++ {
		if i > 0 && isWordRune(folded[i-1]) {
			continue
		}
		if !runesEqual(folded[i:i+len(tr)], tr) {
			continue
		}
		end := i + len(tr)
		if !t.prefix && end < len(folded) && isWordRune(folded[end]) {
			continue
		}
		spans = append(spans, span{i, end})
		if first {
			break
		}
	}
	return spans
}

// matchTerm reports whether t occurs in text (folded, at a word start).
func matchTerm(text string, t hlTerm) bool {
	_, folded := foldRunes(text)
	return len(findSpans(folded, t, true)) > 0
}

func Highlight(text string, terms []hlTerm, maxRunes int) (string, bool) {
	src, folded := foldRunes(text)
	var spans []span
	for _, t := range terms {
		spans = append(spans, findSpans(folded, t, false)...)
	}
	if len(spans) == 0 {
		return "", false
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
	merged := []span{spans[0]}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.a <= last.b {
			last.b = max(last.b, sp.b)
		} else {
			merged = append(merged, sp)
		}
	}
	start, end := 0, len(src)
	if maxRunes > 0 && len(src) > maxRunes {
		start = max(0, merged[0].a-maxRunes/4)
		// Move start to a word boundary.
		for start > 0 && start < merged[0].a && isWordRune(src[start-1]) {
			start++
		}
		end = min(len(src), start+maxRunes)
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	pos := start
	for _, sp := range merged {
		if sp.b <= start || sp.a >= end {
			continue
		}
		a, z := max(sp.a, start), min(sp.b, end)
		b.WriteString(html.EscapeString(string(src[pos:a])))
		b.WriteString("<mark>")
		b.WriteString(html.EscapeString(string(src[a:z])))
		b.WriteString("</mark>")
		pos = z
	}
	b.WriteString(html.EscapeString(string(src[pos:end])))
	if end < len(src) {
		b.WriteString("…")
	}
	return b.String(), true
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
