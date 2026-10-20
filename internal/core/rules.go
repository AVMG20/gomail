package core

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// ruleMatches reports whether a rule's condition holds for a message.
func ruleMatches(r models.LabelRule, accountID, fromName, fromEmail, subject string) bool {
	if r.AccountID != "" && r.AccountID != accountID {
		return false
	}
	needle := strings.ToLower(r.Contains)
	if needle == "" {
		return false
	}
	switch r.Field {
	case "subject":
		return strings.Contains(strings.ToLower(subject), needle)
	default:
		return strings.Contains(strings.ToLower(fromEmail), needle) || strings.Contains(strings.ToLower(fromName), needle)
	}
}

// ruleActions groups what the rules decided: label id -> message ids, and
// the messages to move to Junk.
type ruleActions struct {
	labels  map[string][]string
	junk    []string
	junkWhy map[string]string // message id -> the Junk rule, for the review
}

// ruleWhy describes a Junk rule for the review.
func ruleWhy(r models.LabelRule) string {
	what := "sender"
	if r.Field == "subject" {
		what = "subject"
	}
	return "Your rule: " + what + " contains “" + strings.TrimSpace(r.Contains) + "”"
}

// planRules decides what the rules do to the candidates. Junk rules only
// act when junk is set (new mail): moving mail already on this computer to
// Junk would undo the user's own "not junk" moves. A label rule only adds
// its label to mail of a mailbox the label belongs to (a rule for every
// mailbox skips the others; so does a rule whose label lost its mailbox).
func planRules(rules []models.LabelRule, labels []models.Label, cands []store.RuleCandidate, junk bool) ruleActions {
	act := ruleActions{labels: map[string][]string{}, junkWhy: map[string]string{}}
	byID := make(map[string]models.Label, len(labels))
	for _, l := range labels {
		byID[l.ID] = l
	}
	junked := map[string]bool{}
	for _, c := range cands {
		for _, r := range rules {
			if !ruleMatches(r, c.AccountID, c.FromName, c.FromEmail, c.Subject) {
				continue
			}
			if r.Action == "junk" {
				if junk && !c.InSpam && !junked[c.ID] {
					junked[c.ID] = true
					act.junk = append(act.junk, c.ID)
					act.junkWhy[c.ID] = ruleWhy(r)
				}
			} else if l, ok := byID[r.LabelID]; ok && config.LabelInAccount(l, c.AccountID) {
				act.labels[r.LabelID] = append(act.labels[r.LabelID], c.ID)
			}
		}
	}
	return act
}

// ruleBatch keeps Junk moves and label pushes under the engine's bulk
// limit (a first sync or "Apply to existing mail" can match thousands of
// messages; the engine refuses more than MaxBulk at once).
const ruleBatch = 500

func batches(ids []string, n int) [][]string {
	var out [][]string
	for i := 0; i < len(ids); i += n {
		out = append(out, ids[i:min(i+n, len(ids))])
	}
	return out
}

// applyRuleActions runs the planned actions. It returns how many messages
// changed and the first error of a Junk move (labels are local and only
// fail on database errors, which are logged).
func (s *Service) applyRuleActions(act ruleActions) (int, error) {
	labels := map[string]models.Label{}
	for _, l := range s.Settings().Labels {
		labels[l.ID] = l
	}
	changed := map[string]bool{}
	moves := labelMoves{}
	for labelID, ids := range act.labels {
		l, ok := labels[labelID]
		if !ok {
			continue // the rule's label was deleted
		}
		added, err := s.store.ApplyRuleLabel(ids, labelID)
		if err != nil {
			slog.Warn("label rule failed", "label", labelID, "err", err)
			continue
		}
		for _, id := range added {
			changed[id] = true
			if !slices.Contains(act.junk, id) { // a Junk rule already moves it
				moves.add(id, l, thenKind(l, false), labelWhy(l, false))
			}
		}
		if l.Source == config.LabelSourceProvider {
			for _, b := range batches(added, ruleBatch) {
				s.engine.PushLabel(b, l.Name, true)
			}
		}
	}
	unread, err := s.unreadOf(act.junk)
	if err != nil {
		slog.Warn("junk rule: lookup failed", "err", err)
	}
	moved, moveErr := s.junkRead(unread, act.junk, "junk rule")
	s.logAutoMoves(moved, func(id string) (string, string) { return models.FolderSpam, act.junkWhy[id] })
	moved = append(moved, movedIDs(s.applyLabelMoves(s.ctx, moves, "label action"))...)
	for _, id := range moved {
		changed[id] = true
	}
	if len(changed) > 0 {
		ids := make([]string, 0, len(changed))
		for id := range changed {
			ids = append(ids, id)
		}
		s.messagesChanged(ids)
	}
	return len(changed), moveErr
}

// unreadOf returns the unread messages among ids.
func (s *Service) unreadOf(ids []string) ([]string, error) {
	var out []string
	for _, b := range batches(ids, ruleBatch) {
		sums, err := s.store.Summaries(b)
		if err != nil {
			return out, err
		}
		for _, m := range sums {
			if m.Unread {
				out = append(out, m.ID)
			}
		}
	}
	return out, nil
}

// junkRead marks unread mail read and moves the messages to Junk, in
// batches under the engine's bulk limit, so junked mail doesn't count as
// new (like mail Jev junks). Read first: Outlook gives moved mail new ids.
// Mail a failed move left outside Junk is unread again, so it still
// notifies and counts. It returns the moved ids and the first move error
// (the engine put those messages back where they were).
func (s *Service) junkRead(unread, move []string, what string) ([]string, error) {
	for _, b := range batches(unread, ruleBatch) {
		if err := s.engine.MarkRead(b, true); err != nil {
			slog.Warn(what+": mark read failed", "err", err)
		}
	}
	var moved []string
	var moveErr error
	for _, b := range batches(move, ruleBatch) {
		if err := s.engine.MoveToKind(b, models.FolderSpam); err != nil {
			slog.Warn(what+" failed", "err", err)
			if moveErr == nil {
				moveErr = err
			}
			continue
		}
		moved = append(moved, b...)
	}
	if moveErr != nil && len(unread) > 0 {
		var back []string
		if cands, err := s.store.RuleCandidates(unread); err == nil {
			for _, c := range cands {
				if !c.InSpam {
					back = append(back, c.ID)
				}
			}
		}
		for _, b := range batches(back, ruleBatch) {
			_ = s.engine.MarkRead(b, false)
		}
	}
	return moved, moveErr
}

// applyRules runs the label rules on newly synced mail, before it is
// announced (so quiet labels and Junk moves already count). It only looks
// at the new messages, so a sync doesn't scan the whole mailbox.
func (s *Service) applyRules(accountID string, ids []string) {
	st := s.Settings()
	rules := st.LabelRules
	if len(rules) == 0 || len(ids) == 0 {
		return
	}
	cands, err := s.store.RuleCandidates(ids)
	if err != nil {
		slog.Warn("label rules: reading new mail failed", "account", accountID, "err", err)
		return
	}
	_, _ = s.applyRuleActions(planRules(rules, st.Labels, cands, true))
}

// ApplyLabelRules runs the label rules over the mail already on this
// computer (Settings → Labels & rules → "Apply to existing mail") and returns how
// many messages got a label. Junk rules are skipped: they only act on new
// mail, so mail the user took out of Junk stays out.
func (s *Service) ApplyLabelRules() (int, error) {
	st := s.Settings()
	rules := st.LabelRules
	if len(rules) == 0 {
		return 0, nil
	}
	cands, err := s.store.AllRuleCandidates()
	if err != nil {
		return 0, err
	}
	return s.applyRuleActions(planRules(rules, st.Labels, cands, false))
}

// quietMessage reports whether new mail shouldn't notify: its mailbox has
// notifications off, or it has a quiet label. Junk never notifies (like
// mail in the Spam folder).
func (s *Service) quietMessage(accountID string, m models.MessageSummary) bool {
	st := s.Settings()
	if slices.Contains(st.MutedMailboxes, accountID) {
		return true
	}
	quiet := config.QuietLabels(st, accountID)
	labels := m.Labels
	if !st.ImportProviderLabels {
		// Hidden imported labels still keep mail quiet.
		if all, err := s.store.AllLabelIDs(m.ID); err == nil {
			labels = all
		}
	}
	for _, l := range labels {
		if l == config.JunkLabelID || slices.Contains(quiet, l) {
			return true
		}
	}
	return false
}
