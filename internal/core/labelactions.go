package core

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/store"
)

// Label actions: a label's "When added" (Label.Then) moves mail that a
// rule or Jev gives that label (new mail, or mail the user asked Jev to
// label; labels added by hand too with Settings.ManualLabelActions): to
// the archive (skips the inbox), Junk or Trash. Junk and Trash mark the mail read; archived mail keeps its unread
// state. Jev never trashes: a wrong guess would delete real mail, so its
// Trash picks go to Junk.

// labelMoves maps a message id to where it goes and why (shown in the
// "sorted for you" review).
type labelMoves map[string]labelMove

type labelMove struct {
	kind, why string
	junk      bool // from the built-in Junk label (see Settings.JunkStaysPut)
}

// moveRank orders the destinations when labels disagree: the strongest wins.
var moveRank = map[string]int{models.FolderArchive: 1, models.FolderSpam: 2, models.FolderTrash: 3}

func (m labelMoves) add(id string, l models.Label, kind, why string) {
	junk := l.ID == config.JunkLabelID
	cur := m[id]
	if kind != "" && (moveRank[kind] > moveRank[cur.kind] || (moveRank[kind] == moveRank[cur.kind] && cur.junk && !junk)) {
		m[id] = labelMove{kind, why, junk}
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

// applyLabelMoves moves the messages, skipping mail already where it goes,
// archiving only from the inbox, and never moving sent mail, drafts or
// mail in Trash (a label is about received mail; Trash is the user's own
// choice). It stops between batches when ctx ends. It returns the moved
// ids: kind -> ids.
func (s *Service) applyLabelMoves(ctx context.Context, moves labelMoves, what string) map[string][]string {
	if len(moves) == 0 {
		return nil
	}
	ids := make([]string, 0, len(moves))
	for id := range moves {
		ids = append(ids, id)
	}
	places, err := s.store.FolderKinds(ids)
	if err != nil {
		slog.Warn(what+": lookup failed", "err", err)
		return nil
	}
	junkStays := s.Settings().JunkStaysPut
	byKind := map[string][]string{}
	var readFirst []string // Junk and Trash moves
	for _, id := range ids {
		p, ok := places[id]
		in, kind := p.Kinds, moves[id].kind
		if !ok || (moves[id].junk && slices.Contains(junkStays, p.AccountID)) || slices.Contains(in, kind) || slices.Contains(in, models.FolderSent) || slices.Contains(in, models.FolderDrafts) ||
			slices.Contains(in, models.FolderTrash) || (kind == models.FolderArchive && !slices.Contains(in, models.FolderInbox)) {
			continue
		}
		byKind[kind] = append(byKind[kind], id)
		if kind != models.FolderArchive {
			readFirst = append(readFirst, id)
		}
	}
	unread, err := s.unreadOf(readFirst)
	if err != nil {
		slog.Warn(what+": lookup failed", "err", err)
	}
	isUnread := make(map[string]bool, len(unread))
	for _, id := range unread {
		isUnread[id] = true
	}
	unreadIn := func(ids []string) []string {
		var out []string
		for _, id := range ids {
			if isUnread[id] {
				out = append(out, id)
			}
		}
		return out
	}
	moved := map[string][]string{}
	// Junk and Trash: read first, then move.
	if ctx.Err() == nil {
		junked, _ := s.junkRead(unreadIn(byKind[models.FolderSpam]), byKind[models.FolderSpam], what)
		if len(junked) > 0 {
			moved[models.FolderSpam] = junked
		}
	}
	for _, kind := range []string{models.FolderTrash, models.FolderArchive} {
		if kind == models.FolderTrash {
			for _, b := range batches(unreadIn(byKind[kind]), ruleBatch) {
				if err := s.engine.MarkRead(b, true); err != nil {
					slog.Warn(what+": mark read failed", "err", err)
				}
			}
		}
		for _, b := range batches(byKind[kind], ruleBatch) {
			if ctx.Err() != nil {
				break
			}
			if err := s.engine.MoveToKind(b, kind); err != nil {
				slog.Warn(what+": move failed", "to", kind, "err", err)
				continue
			}
			moved[kind] = append(moved[kind], b...)
		}
	}
	s.logAutoMoves(movedIDs(moved), func(id string) (string, string) { return moves[id].kind, moves[id].why })
	return moved
}

// ApplyLabelAction does a label's "When added" once for the mail that
// already has it (set after the fact), and returns how many messages
// moved. Sent mail, drafts and Trash stay put; Archive only takes mail
// from the inbox.
func (s *Service) ApplyLabelAction(labelID string) (int, error) {
	l, err := s.label(labelID)
	if err != nil {
		return 0, err
	}
	kind := thenKind(l, false)
	if kind == "" || l.ID == config.JunkLabelID || len(l.AccountIDs) != 1 {
		return 0, errors.New("this label doesn’t move mail")
	}
	ids, err := s.store.ResolveIDs(models.ListQuery{AccountID: l.AccountIDs[0], Labels: []string{l.ID}},
		store.FilterOptions{ExcludeFolderKinds: []string{models.FolderSent, models.FolderDrafts, models.FolderTrash, kind}}, provider.MaxFolderOp)
	if err != nil {
		return 0, err
	}
	moves := labelMoves{}
	for _, id := range ids {
		moves.add(id, l, kind, "Labeled “"+l.Name+"”")
	}
	moved := movedIDs(s.applyLabelMoves(s.ctx, moves, "label action"))
	s.messagesChanged(moved)
	return len(moved), nil
}

// movedIDs flattens applyLabelMoves' result.
func movedIDs(moved map[string][]string) []string {
	var out []string
	for _, ids := range moved {
		out = append(out, ids...)
	}
	return out
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
