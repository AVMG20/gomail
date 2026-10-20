package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/store"
)

// Classification limits.
const (
	classifyWorkers   = 4
	maxSelection      = 200   // ClassifyMessages
	maxJobMessages    = 50000 // ClassifyQuery
	maxAutoPerSync    = 100
	maxAutoInitial    = 30
	selectionJobID    = "selection"
	autoJobID         = "auto"
	progressEveryMsgs = 5
)

// excluded from bulk labeling unless includeAllFolders.
var bulkExcludedKinds = []string{models.FolderTrash, models.FolderDrafts}

type job struct {
	id     string
	cancel context.CancelFunc
}

type autoRun struct {
	cancel context.CancelFunc
	ids    []string // the batch's messages (classified ones are skipped)
}

type jobManager struct {
	mu        sync.Mutex
	current   *job
	others    map[string]*autoRun // automatic labeling per account
	budgetDay string              // day the budget event was last emitted
}

func (j *jobManager) init() { j.others = map[string]*autoRun{} }

func (j *jobManager) cancelAll() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.current != nil {
		j.current.cancel()
	}
	for _, r := range j.others {
		r.cancel()
	}
}

// ---- simple wrappers ----

// TestOpenRouter validates the stored key.
func (s *Service) TestOpenRouter() error {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	return s.ai.TestKey(ctx)
}

// ListAIModels returns the (cached) OpenRouter model list.
func (s *Service) ListAIModels(force bool) ([]models.AIModel, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	return s.ai.ListModels(ctx, force)
}

func (s *Service) chatModel() string { return s.Settings().OpenRouterModel }

// ImproveGrammar fixes spelling and grammar with the chat model.
func (s *Service) ImproveGrammar(text string) (string, error) {
	if len(text) > 100_000 {
		return "", errors.New("the text is too long to check at once")
	}
	st := s.Settings()
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	return s.ai.ImproveGrammar(ctx, st.OpenRouterModel, st.GrammarSystemPrompt, text)
}

func emailForPrompt(m models.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s <%s>\nSubject: %s\nDate: %s\n\n", m.From.Name, m.From.Email, m.Subject, m.Date.Format(time.RFC1123))
	body := m.BodyText
	if body == "" {
		body = m.Snippet
	}
	b.WriteString(ai.CleanBody(body, 8000))
	return b.String()
}

// ParseEventText fills a new event from a natural-language description with
// the chat model chosen in Settings → AI.
// timeZone is the UI's IANA zone (Go's local zone has no usable name).
func (s *Service) ParseEventText(text, timeZone string) (models.EventDraft, error) {
	now := time.Now()
	if loc, err := time.LoadLocation(timeZone); err == nil && timeZone != "" {
		now = now.In(loc)
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()
	return s.ai.ParseEvent(ctx, s.chatModel(), text, now)
}

// SummarizeMessage returns a short bullet summary.
func (s *Service) SummarizeMessage(id string) (string, error) {
	m, err := s.Message(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	return s.ai.Summarize(ctx, s.chatModel(), emailForPrompt(m))
}

// ---- classification ----

// labelDefs are the labels offered to the classifier for mail of an
// account (see labelDefsOf).
func (s *Service) labelDefs(accountID string) []ai.LabelDef {
	return labelDefsOf(s.Settings().Labels, accountID)
}

// labelDefsOf are the labels of a mailbox ("" = every label) that the
// classifier is offered. Imported Gmail labels and Outlook categories
// need a description first: from a name alone ("Categorie Rood") the
// classifier can only guess.
func labelDefsOf(labels []models.Label, accountID string) []ai.LabelDef {
	var out []ai.LabelDef
	for _, l := range config.LabelsForAccount(labels, accountID) {
		if l.Source == config.LabelSourceProvider && strings.TrimSpace(l.Description) == "" {
			continue
		}
		out = append(out, ai.LabelDef{ID: l.ID, Name: l.Name, Description: l.Description})
	}
	return out
}

// policy is how scores change the labels of mail of an account.
func (s *Service) policy(accountID string) store.AIPolicy {
	st := s.Settings()
	return store.AIPolicy{AddThreshold: st.ClassifyThreshold, RemoveThreshold: st.ClassifyRemoveThreshold, CorrectProvider: config.AICorrects(st, accountID)}
}

func (s *Service) checkAIReady() error {
	key, err := s.openRouterKey()
	if err != nil {
		return err
	}
	if key == "" {
		return ai.ErrNoKey
	}
	if len(s.Settings().Labels) == 0 {
		return errors.New("create at least one label in Settings first")
	}
	return nil
}

// labelsetHash fingerprints everything that influences classification of
// an account's mail besides the message: model, instructions and the
// definitions of the labels offered for that mailbox.
func (s *Service) labelsetHash(accountID string) string {
	st := s.Settings()
	return labelsetHashOf(st, labelDefsOf(st.Labels, accountID))
}

func labelsetHashOf(st models.Settings, defs []ai.LabelDef) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", st.ClassifierModel, st.ClassifyInstructions)
	defs = slices.Clone(defs)
	sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
	for _, d := range defs {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", d.ID, d.Name, d.Description)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// aiHash fingerprints a message for the classifier: label set + the
// message's identity (mail is immutable; the body arriving later doesn't
// trigger a re-run).
func aiHash(labelset string, in store.ClassifyInput) string {
	h := sha256.Sum256([]byte(in.Subject + "\x00" + strings.ToLower(in.From.Email) + "\x00" + in.Date.UTC().Format(time.RFC3339Nano)))
	return labelset + ":" + hex.EncodeToString(h[:])[:12]
}

// classifyOne scores and applies labels for one message. Messages already
// classified with the current label set are skipped (no cost).
func (s *Service) classifyOne(ctx context.Context, in store.ClassifyInput) models.ClassificationResult {
	res := models.ClassificationResult{MessageID: in.ID, Added: []string{}, Removed: []string{}, Scores: map[string]float64{}}
	hash := aiHash(s.labelsetHash(in.AccountID), in)
	if in.AIHash == hash && in.AIScores != nil {
		res.Scores, res.Skipped = in.AIScores, true
		// No new scores needed, but the thresholds or provider-label
		// policy may have changed since: apply them to the stored scores
		// of the labels the classifier is offered now.
		offered := map[string]float64{}
		for _, d := range s.labelDefs(in.AccountID) {
			if p, ok := in.AIScores[d.ID]; ok {
				offered[d.ID] = p
			}
		}
		added, removed, err := s.store.ApplyAIScores(in.ID, offered, s.policy(in.AccountID), hash)
		if err == nil {
			res.Added, res.Removed = added, removed
		}
		return res
	}
	st := s.Settings()
	body := in.BodyText
	if strings.TrimSpace(body) == "" {
		body = in.Snippet
	}
	scores, err := s.ai.ScoreLabels(ctx, st.ClassifierModel, st.ClassifyInstructions, s.labelDefs(in.AccountID), ai.Email{
		From: in.From, To: in.To, Subject: in.Subject, Date: in.Date, HasAttachments: in.HasAttachments, Body: body,
	})
	res.Cost = scores.Cost
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Scores = scores.Scores
	added, removed, err := s.store.ApplyAIScores(in.ID, scores.Scores, s.policy(in.AccountID), hash)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Added, res.Removed = added, removed
	return res
}

type runStats struct {
	mu   sync.Mutex
	p    models.ClassifyProgress
	accs map[string]bool
}

// runClassification classifies ids with bounded concurrency, emitting
// progress. It returns the per-message results in input order.
func (s *Service) runClassification(ctx context.Context, jobID string, ids []string, skipClassified bool) ([]models.ClassificationResult, models.ClassifyProgress) {
	stats := &runStats{p: models.ClassifyProgress{JobID: jobID, Total: len(ids)}, accs: map[string]bool{}}
	results := make([]models.ClassificationResult, len(ids))
	s.emit(models.EventClassifyProgress, stats.p)
	accountOf := map[string]string{}
	if sums, err := s.store.Summaries(ids); err == nil {
		for _, m := range sums {
			accountOf[m.ID] = m.AccountID
		}
	}
	flush := func() {
		stats.mu.Lock()
		accs := stats.accs
		stats.accs = map[string]bool{}
		stats.mu.Unlock()
		for a := range accs {
			s.emit(models.EventMessagesChanged, models.AccountEvent{AccountID: a})
		}
		if len(accs) > 0 {
			s.emit(models.EventLabelsChanged, models.AccountEvent{})
		}
	}
	var fatal error
	var fatalOnce sync.Once
	jctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := provider.Parallel(jctx, classifyWorkers, indexes(len(ids)), func(ctx context.Context, i int) error {
		inputs, err := s.store.ClassifyInputs([]string{ids[i]})
		var r models.ClassificationResult
		switch {
		case err != nil:
			r = models.ClassificationResult{MessageID: ids[i], Error: err.Error()}
		case len(inputs) == 0:
			r = models.ClassificationResult{MessageID: ids[i], Error: "message no longer exists"}
		case skipClassified && !inputs[0].ClassifiedAt.IsZero():
			r = models.ClassificationResult{MessageID: ids[i]}
			stats.mu.Lock()
			stats.p.Skipped++
			stats.p.Done++
			stats.mu.Unlock()
			results[i] = r
			return nil
		case jobID == autoJobID && s.budgetReached():
			s.announceBudget()
			cancel()
			return nil
		default:
			r = s.classifyOne(ctx, inputs[0])
		}
		results[i] = r
		stats.mu.Lock()
		stats.p.Done++
		stats.p.Cost += r.Cost
		if r.Skipped {
			stats.p.Skipped++
			if len(r.Added)+len(r.Removed) > 0 {
				stats.accs[accountOf[ids[i]]] = true // policy re-applied
			}
		} else if r.Error != "" {
			stats.p.Failed++
		} else if len(r.Added)+len(r.Removed) > 0 {
			stats.p.Labeled++
			stats.accs[accountOf[ids[i]]] = true
		}
		snapshot := stats.p
		stats.mu.Unlock()
		if snapshot.Done%progressEveryMsgs == 0 || snapshot.Done == snapshot.Total {
			s.emit(models.EventClassifyProgress, snapshot)
			flush()
		}
		// Account/key problems affect every message: stop early.
		if r.Error != "" && isFatalAIError(r.Error) {
			fatalOnce.Do(func() { fatal = errors.New(r.Error) })
			cancel()
		}
		return nil
	})
	flush()
	stats.mu.Lock()
	final := stats.p
	stats.mu.Unlock()
	if fatal != nil {
		final.Error = fatal.Error()
	} else if err != nil || ctx.Err() != nil {
		final.Cancelled = true
	}
	return results, final
}

func isFatalAIError(msg string) bool {
	for _, s := range []string{"API key", "insufficient credits", "not a decision model", "OpenRouter key", "could not find that model"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// ClassifyMessages labels an explicit selection and returns the results.
func (s *Service) ClassifyMessages(ids []string) ([]models.ClassificationResult, error) {
	if err := s.checkAIReady(); err != nil {
		return nil, err
	}
	ids, err := s.expand(ids, store.ExpandAnchor)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.ClassificationResult{}, nil
	}
	if len(ids) > maxSelection {
		return nil, fmt.Errorf("select at most %d messages, or label everything matching the current view instead", maxSelection)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	if len(ids) == 1 {
		inputs, err := s.store.ClassifyInputs(ids)
		if err != nil {
			return nil, err
		}
		if len(inputs) == 0 {
			return nil, errors.New("that message no longer exists")
		}
		r := s.classifyOne(ctx, inputs[0])
		if r.Error != "" {
			return nil, errors.New(r.Error)
		}
		results := []models.ClassificationResult{r}
		s.moveAILabeled(ctx, results)
		s.messagesChanged(ids)
		return results, nil
	}
	results, final := s.runClassification(ctx, selectionJobID, ids, false)
	s.moveAILabeled(ctx, results)
	s.emit(models.EventClassifyFinished, final)
	if final.Error != "" && final.Failed == len(ids) {
		return results, errors.New(final.Error)
	}
	return results, nil
}

func (s *Service) bulkOptions(onlyUnlabeled, includeAllFolders bool) store.FilterOptions {
	opt := store.FilterOptions{OnlyUnlabeled: onlyUnlabeled}
	if !includeAllFolders {
		opt.ExcludeFolderKinds = bulkExcludedKinds
	}
	return opt
}

// ClassifyQuery starts a background job labeling every message matching q.
func (s *Service) ClassifyQuery(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyJob, error) {
	if err := s.checkAIReady(); err != nil {
		return models.ClassifyJob{}, err
	}
	q.Cursor = ""
	ids, err := s.store.ResolveIDs(q, s.bulkOptions(onlyUnlabeled, includeAllFolders), maxJobMessages)
	if err != nil {
		return models.ClassifyJob{}, err
	}
	if len(ids) == 0 {
		return models.ClassifyJob{}, errors.New("no messages match; nothing to label")
	}
	s.jobs.mu.Lock()
	if s.jobs.current != nil {
		s.jobs.mu.Unlock()
		return models.ClassifyJob{}, errors.New("another labeling job is still running; cancel it or wait until it finishes")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	j := &job{id: randomID("job_"), cancel: cancel}
	s.jobs.current = j
	s.jobs.mu.Unlock()

	s.goBG(func() {
		defer cancel()
		results, final := s.runClassification(ctx, j.id, ids, false)
		s.moveAILabeled(ctx, results) // still the current job: Cancel stops the moves too
		s.jobs.mu.Lock()
		if s.jobs.current == j {
			s.jobs.current = nil
		}
		s.jobs.mu.Unlock()
		slog.Info("labeling job finished", "job", j.id, "done", final.Done, "labeled", final.Labeled, "failed", final.Failed, "cost", final.Cost)
		s.emit(models.EventClassifyFinished, final)
	})
	return models.ClassifyJob{JobID: j.id, Total: len(ids)}, nil
}

// CancelClassify stops a running job.
func (s *Service) CancelClassify(jobID string) error {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	if jobID == autoJobID {
		for _, r := range s.jobs.others {
			r.cancel()
		}
		return nil
	}
	if s.jobs.current == nil || (jobID != "" && s.jobs.current.id != jobID) {
		return errors.New("that labeling job is not running")
	}
	s.jobs.current.cancel()
	return nil
}

// EstimateClassify estimates the size and cost of ClassifyQuery.
func (s *Service) EstimateClassify(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyEstimate, error) {
	q.Cursor = ""
	prefixes := map[string]string{}
	for _, id := range s.accountIDs() {
		prefixes[id] = s.labelsetHash(id) + ":"
	}
	count, unchanged, chars, err := s.store.EstimateInput(q, s.bulkOptions(onlyUnlabeled, includeAllFolders), ai.MaxBodyChars, prefixes)
	if err != nil {
		return models.ClassifyEstimate{}, err
	}
	est := models.ClassifyEstimate{Count: count, Unchanged: unchanged}
	usage, _ := s.AIUsage()
	est.TodayUSD = usage.TodayUSD
	todo := count - unchanged
	if todo <= 0 {
		est.PriceKnown = true
		return est, nil
	}
	st := s.Settings()
	perMsg, reqs := ai.EstimateTokens(int(chars/int64(todo)), s.labelDefs(q.AccountID), st.ClassifyInstructions)
	est.Requests = reqs * todo
	est.EstimatedTokens = perMsg * todo
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if m, ok := s.ai.Model(ctx, st.ClassifierModel); ok && !m.PriceUnknown {
		est.PriceKnown = true
		est.EstimatedCostUSD = float64(est.EstimatedTokens) * m.PromptPrice / 1e6
	}
	est.ExceedsBudget = usage.DailyBudgetUSD > 0 && usage.TodayUSD+est.EstimatedCostUSD > usage.DailyBudgetUSD
	return est, nil
}

// onNewMessages auto-labels newly synced incoming mail.
func (s *Service) onNewMessages(accountID string, ids []string, initial bool) {
	st := s.Settings()
	if !config.AutoClassifies(st, accountID) || s.checkAIReady() != nil {
		return
	}
	if s.budgetReached() {
		s.announceBudget()
		return
	}
	s.announceResume()
	inputs, err := s.store.ClassifyInputs(ids)
	if err != nil {
		return
	}
	// Incoming mail only: not drafts, trash or sent; newest first.
	var eligible []store.ClassifyInput
	for _, in := range inputs {
		if !in.ClassifiedAt.IsZero() || len(in.FolderKinds) == 0 {
			continue
		}
		incoming := false
		skip := false
		for _, k := range in.FolderKinds {
			switch k {
			case models.FolderInbox, models.FolderCustom, models.FolderSpam:
				incoming = true
			case models.FolderTrash, models.FolderDrafts:
				skip = true
			}
		}
		if incoming && !skip {
			eligible = append(eligible, in)
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Date.After(eligible[j].Date) })
	limit := maxAutoPerSync
	if initial {
		limit = maxAutoInitial
	}
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	if len(eligible) == 0 {
		return
	}
	todo := make([]string, len(eligible))
	for i, in := range eligible {
		todo[i] = in.ID
	}
	s.goBG(func() {
		ctx, cancel := context.WithCancel(s.ctx)
		run := &autoRun{cancel: cancel, ids: todo}
		s.jobs.mu.Lock()
		if prev, ok := s.jobs.others[accountID]; ok {
			// A newer batch supersedes the old one for this account and
			// takes over what it hadn't labeled yet (already classified
			// messages are skipped).
			prev.cancel()
			run.ids = mergeIDs(todo, prev.ids, maxAutoPerSync)
		}
		s.jobs.others[accountID] = run
		todo := run.ids
		s.jobs.mu.Unlock()
		defer func() {
			cancel()
			s.jobs.mu.Lock()
			if s.jobs.others[accountID] == run {
				delete(s.jobs.others, accountID)
			}
			s.jobs.mu.Unlock()
		}()
		results, final := s.runClassification(ctx, autoJobID, todo, true)
		s.moveAILabeled(ctx, results)
		s.emit(models.EventClassifyFinished, final)
	})
}

// moveAILabeled acts on the labels Jev just gave mail (new mail, or a
// selection or view the user asked Jev to label): Junk (and a label set to
// "When added: Junk") moves it to Junk, read; other label actions archive
// it (see labelactions.go). It only acts on labels Jev added in this run:
// mail the user took out of Junk isn't moved back by a later run.
// It sets each result's Moved.
func (s *Service) moveAILabeled(ctx context.Context, results []models.ClassificationResult) {
	labels := map[string]models.Label{}
	for _, l := range s.Settings().Labels {
		labels[l.ID] = l
	}
	moves := labelMoves{}
	var ids []string
	for _, r := range results {
		if r.Skipped || r.Error != "" || !slices.Contains(r.Added, config.JunkLabelID) {
			continue
		}
		ids = append(ids, r.MessageID)
	}
	stays := s.junkStaysFor(ids)
	for _, r := range results {
		if r.Skipped || r.Error != "" {
			continue
		}
		for _, id := range r.Added {
			if l, ok := labels[id]; ok {
				moves.add(r.MessageID, l, thenKind(l, true), labelWhy(l, true), stays)
			}
		}
	}
	moved := s.applyLabelMoves(ctx, moves, "AI label action")
	if len(moved) == 0 {
		return
	}
	kindOf := map[string]string{}
	for kind, ids := range moved {
		for _, id := range ids {
			kindOf[id] = kind
		}
	}
	for i := range results {
		results[i].Moved = kindOf[results[i].MessageID]
	}
	s.messagesChanged(movedIDs(moved))
}

// mergeIDs returns a followed by the ids of b not in a, at most limit.
func mergeIDs(a, b []string, limit int) []string {
	seen := make(map[string]bool, len(a))
	out := make([]string, 0, min(len(a)+len(b), limit))
	for _, l := range [][]string{a, b} {
		for _, id := range l {
			if len(out) >= limit {
				return out
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// ---- usage & budget ----

func (s *Service) recordUsage(u ai.Usage) {
	if err := s.store.RecordAIUsage(u.Kind, u.Model, u.Cost, u.InputTokens, u.OutputTokens); err != nil {
		slog.Warn("recording AI usage", "err", err)
	}
}

// AIUsage returns today's and this month's recorded spending.
func (s *Service) AIUsage() (models.AIUsage, error) {
	u, err := s.store.AIUsage(time.Now())
	if err != nil {
		return u, err
	}
	u.DailyBudgetUSD = s.Settings().AIDailyBudgetUSD
	u.BudgetReached = u.DailyBudgetUSD > 0 && u.TodayUSD >= u.DailyBudgetUSD
	return u, nil
}

func (s *Service) budgetReached() bool {
	u, err := s.AIUsage()
	return err == nil && u.BudgetReached
}

// announceBudget emits ai:budget once per day.
func (s *Service) announceBudget() {
	day := time.Now().Format("2006-01-02")
	s.jobs.mu.Lock()
	first := s.jobs.budgetDay != day
	s.jobs.budgetDay = day
	s.jobs.mu.Unlock()
	if first {
		u, _ := s.AIUsage()
		slog.Info("AI daily budget reached; automatic labeling paused until tomorrow", "spent", u.TodayUSD, "budget", u.DailyBudgetUSD)
		s.emit(models.EventAIBudget, models.AIBudgetEvent{Paused: true, TodayUSD: u.TodayUSD, BudgetUSD: u.DailyBudgetUSD})
	}
}

// announceResume emits ai:budget {paused:false} once automatic labeling
// runs again after a pause (next day or a raised budget).
func (s *Service) announceResume() {
	s.jobs.mu.Lock()
	was := s.jobs.budgetDay != ""
	s.jobs.budgetDay = ""
	s.jobs.mu.Unlock()
	if was {
		u, _ := s.AIUsage()
		s.emit(models.EventAIBudget, models.AIBudgetEvent{Paused: false, TodayUSD: u.TodayUSD, BudgetUSD: u.DailyBudgetUSD})
	}
}
