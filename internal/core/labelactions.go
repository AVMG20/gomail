package core

import (
	"log/slog"
	"slices"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// Label actions: a label's "When added" (Label.Then) moves mail that gets
// that label from a rule, Jev or the user: to the archive (skips the
// inbox), Junk or Trash. Junk and Trash mark the mail read; archived mail keeps its unread
// state. Jev never trashes: a wrong guess would delete real mail, so its
// Trash picks go to Junk.

// labelMoves maps a message id to where it goes and why (shown in the
// "sorted for you" review).
type labelMoves map[string]labelMove

type labelMove struct{ kind, why string }

// moveRank orders the destinations when labels disagree: the strongest wins.
var moveRank = map[string]int{models.FolderArchive: 1, models.FolderSpam: 2, models.FolderTrash: 3}

func (m labelMoves) add(id, kind, why string) {
	if kind != "" && moveRank[kind] > moveRank[m[id].kind] {
		m[id] = labelMove{kind, why}
	}
}

// labelWhy says why a label moved mail, for the review.
func labelWhy(l models.Label, byAI bool) string {
	switch {
	case l.ID == config.JunkLabelID && byAI:
		return "Jev thinks it’s junk"
	case byAI:
		return "Jev labeled it “" + l.Name + "”"
	default:
		return "Your rule labeled it “" + l.Name + "”"
	}
}

// thenKind is the folder kind a label sends new mail to ("" = stays put).
func thenKind(l models.Label, byAI bool) string {
	if l.ID == config.JunkLabelID {
		return models.FolderSpam
	}
	switch l.Then {
	case "archive":
		return models.FolderArchive
	case "junk":
		return models.FolderSpam
	case "trash":
		if byAI {
			return models.FolderSpam
		}
		return models.FolderTrash
	}
	return ""
}

// applyLabelMoves moves the messages, skipping mail already where it goes
// (and archiving only from the inbox). It returns the moved ids.
func (s *Service) applyLabelMoves(moves labelMoves, what string) []string {
	if len(moves) == 0 {
		return nil
	}
	ids := make([]string, 0, len(moves))
	for id := range moves {
		ids = append(ids, id)
	}
	inputs, err := s.store.ClassifyInputs(ids)
	if err != nil {
		slog.Warn(what+": lookup failed", "err", err)
		return nil
	}
	byKind := map[string][]string{}
	var readFirst []string // Junk and Trash moves
	for _, in := range inputs {
		kind := moves[in.ID].kind
		if slices.Contains(in.FolderKinds, kind) || (kind == models.FolderArchive && !slices.Contains(in.FolderKinds, models.FolderInbox)) {
			continue
		}
		byKind[kind] = append(byKind[kind], in.ID)
		if kind != models.FolderArchive {
			readFirst = append(readFirst, in.ID)
		}
	}
	unread, err := s.unreadOf(readFirst)
	if err != nil {
		slog.Warn(what+": lookup failed", "err", err)
	}
	var moved []string
	// Junk and Trash: read first, then move.
	junked, _ := s.junkRead(filterIn(unread, byKind[models.FolderSpam]), byKind[models.FolderSpam], what)
	moved = append(moved, junked...)
	for _, kind := range []string{models.FolderTrash, models.FolderArchive} {
		if kind == models.FolderTrash {
			for _, b := range batches(filterIn(unread, byKind[kind]), ruleBatch) {
				if err := s.engine.MarkRead(b, true); err != nil {
					slog.Warn(what+": mark read failed", "err", err)
				}
			}
		}
		for _, b := range batches(byKind[kind], ruleBatch) {
			if err := s.engine.MoveToKind(b, kind); err != nil {
				slog.Warn(what+": move failed", "to", kind, "err", err)
				continue
			}
			moved = append(moved, b...)
		}
	}
	var logged []string // moves without a reason were the user's own
	for _, id := range moved {
		if moves[id].why != "" {
			logged = append(logged, id)
		}
	}
	s.logAutoMoves(logged, func(id string) (string, string) { return moves[id].kind, moves[id].why })
	return moved
}

// logAutoMoves records what an automation moved, for the "sorted for you"
// review (best effort: a failed log only loses the review entry).
func (s *Service) logAutoMoves(ids []string, info func(id string) (kind, why string)) {
	if len(ids) == 0 {
		return
	}
	sums, err := s.store.Summaries(ids)
	if err != nil {
		return
	}
	entries := make([]store.AutoMoveEntry, 0, len(sums))
	for _, m := range sums {
		kind, why := info(m.ID)
		entries = append(entries, store.AutoMoveEntry{MessageID: m.ID, AccountID: m.AccountID, ToKind: kind, Reason: why})
	}
	if err := s.store.RecordAutoMoves(entries, time.Now()); err != nil {
		slog.Debug("auto moves log failed", "err", err)
	}
}

// AutoMoves lists what automations moved into a folder kind (spam,
// archive, trash) in the last hours ("" account = every mailbox), newest
// first, for the "sorted for you" review.
func (s *Service) AutoMoves(accountID, kind string, hours int) ([]models.AutoMove, error) {
	if hours <= 0 {
		hours = 24
	}
	return s.store.AutoMoves(accountID, kind, time.Now().Add(-time.Duration(hours)*time.Hour), 500)
}

// filterIn returns the ids of a that are in b.
func filterIn(a, b []string) []string {
	var out []string
	for _, id := range a {
		if slices.Contains(b, id) {
			out = append(out, id)
		}
	}
	return out
}
