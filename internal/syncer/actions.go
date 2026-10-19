package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/store"
)

// MaxBulk caps the number of messages one action may touch.
const MaxBulk = 5000

// group loads summaries for ids and groups them by account.
func (e *Engine) group(ids []string) (map[string][]models.MessageSummary, error) {
	if len(ids) > MaxBulk {
		return nil, fmt.Errorf("too many messages selected (max %d)", MaxBulk)
	}
	sums, err := e.cfg.Store.Summaries(ids)
	if err != nil {
		return nil, err
	}
	out := map[string][]models.MessageSummary{}
	for _, m := range sums {
		out[m.AccountID] = append(out[m.AccountID], m)
	}
	return out, nil
}

func remoteIDs(ms []models.MessageSummary) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.RemoteID
	}
	return out
}

func localIDs(ms []models.MessageSummary) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func (e *Engine) changed(accounts ...string) {
	for _, a := range accounts {
		e.cfg.Emit.Emit(models.EventMessagesChanged, models.AccountEvent{AccountID: a})
	}
	e.cfg.Emit.Emit(models.EventAccountsChanged, models.AccountEvent{})
}

// perAccount runs push for each account concurrently; failed accounts are
// reverted with revert. It returns a combined human-readable error.
func (e *Engine) perAccount(groups map[string][]models.MessageSummary, what string,
	push func(ctx context.Context, accountID string, ms []models.MessageSummary) error,
	revert func(accountID string, ms []models.MessageSummary)) error {
	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup
	for acct, ms := range groups {
		wg.Add(1)
		go func(acct string, ms []models.MessageSummary) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
			defer cancel()
			if err := safe.Call(what, func() error { return push(ctx, acct, ms) }); err != nil {
				slog.Warn("action failed", "action", what, "account", acct, "err", err)
				revert(acct, ms)
				a, _ := e.cfg.Store.Account(acct)
				mu.Lock()
				errs = append(errs, fmt.Sprintf("Couldn't %s on %s: %s", what, a.Email, HumanError(a, err)))
				mu.Unlock()
				e.changed(acct)
			}
		}(acct, ms)
	}
	wg.Wait()
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New(joinErrs(errs))
	}
	return nil
}

func joinErrs(errs []string) string {
	out := errs[0]
	for _, e := range errs[1:] {
		out += "; " + e
	}
	return out
}

func keys(m map[string][]models.MessageSummary) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func filterChanged(groups map[string][]models.MessageSummary, changed []string) map[string][]models.MessageSummary {
	set := map[string]bool{}
	for _, id := range changed {
		set[id] = true
	}
	out := map[string][]models.MessageSummary{}
	for a, ms := range groups {
		for _, m := range ms {
			if set[m.ID] {
				out[a] = append(out[a], m)
			}
		}
	}
	return out
}

// MarkRead marks messages read/unread.
func (e *Engine) MarkRead(ids []string, read bool) error {
	groups, err := e.group(ids)
	if err != nil {
		return err
	}
	changed, err := e.cfg.Store.SetUnread(ids, !read)
	if err != nil {
		return err
	}
	groups = filterChanged(groups, changed)
	if len(groups) == 0 {
		return nil
	}
	e.changed(keys(groups)...)
	what := "mark messages as read"
	if !read {
		what = "mark messages as unread"
	}
	return e.perAccount(groups, what, func(ctx context.Context, acct string, ms []models.MessageSummary) error {
		p, err := e.cfg.Provider(acct)
		if err != nil {
			return err
		}
		return p.SetRead(ctx, remoteIDs(ms), read)
	}, func(acct string, ms []models.MessageSummary) {
		_, _ = e.cfg.Store.SetUnread(localIDs(ms), read)
	})
}

// SetStarred stars/flags messages.
func (e *Engine) SetStarred(ids []string, starred bool) error {
	groups, err := e.group(ids)
	if err != nil {
		return err
	}
	changed, err := e.cfg.Store.SetStarred(ids, starred)
	if err != nil {
		return err
	}
	groups = filterChanged(groups, changed)
	if len(groups) == 0 {
		return nil
	}
	e.changed(keys(groups)...)
	return e.perAccount(groups, "update the star", func(ctx context.Context, acct string, ms []models.MessageSummary) error {
		p, err := e.cfg.Provider(acct)
		if err != nil {
			return err
		}
		return p.SetStarred(ctx, remoteIDs(ms), starred)
	}, func(acct string, ms []models.MessageSummary) {
		_, _ = e.cfg.Store.SetStarred(localIDs(ms), !starred)
	})
}

// MoveToKind moves messages of every account to that account's folder of
// the given kind (archive, trash, inbox, spam).
func (e *Engine) MoveToKind(ids []string, kind string) error {
	return e.move(ids, func(acct string) (models.Folder, error) {
		f, err := e.cfg.Store.FolderByKind(acct, kind)
		if errors.Is(err, store.ErrNotFound) {
			a, _ := e.cfg.Store.Account(acct)
			return f, fmt.Errorf("%s has no %s folder", a.Email, kind)
		}
		return f, err
	})
}

// MoveToFolder moves messages to a specific folder (same account only).
func (e *Engine) MoveToFolder(ids []string, folderID string) error {
	target, err := e.cfg.Store.Folder(folderID)
	if err != nil {
		return errors.New("that folder no longer exists")
	}
	return e.move(ids, func(acct string) (models.Folder, error) {
		if acct != target.AccountID {
			return target, errors.New("messages can only be moved to folders of their own account")
		}
		return target, nil
	})
}

func (e *Engine) move(ids []string, targetFor func(acct string) (models.Folder, error)) error {
	groups, err := e.group(ids)
	if err != nil {
		return err
	}
	targets := map[string]models.Folder{}
	for acct := range groups {
		t, err := targetFor(acct)
		if err != nil {
			return err
		}
		targets[acct] = t
	}
	current, err := e.cfg.Store.MessageFolders(ids)
	if err != nil {
		return err
	}
	newSets := map[string][]string{}
	for acct, ms := range groups {
		p, err := e.cfg.Provider(acct)
		if err != nil {
			return err
		}
		folders, err := e.cfg.Store.Folders(acct)
		if err != nil {
			return err
		}
		remoteOf, localOf := map[string]string{}, map[string]string{}
		for _, f := range folders {
			remoteOf[f.ID], localOf[f.RemoteID] = f.RemoteID, f.ID
		}
		t := targets[acct]
		for _, m := range ms {
			var cur []string
			for _, fid := range current[m.ID] {
				cur = append(cur, remoteOf[fid])
			}
			var next []string
			for _, r := range p.PlanMove(cur, mail.Folder{RemoteID: t.RemoteID, Name: t.Name, Kind: t.Kind}) {
				if lid, ok := localOf[r]; ok {
					next = append(next, lid)
				}
			}
			if len(next) == 0 {
				next = current[m.ID] // never orphan a message locally
			}
			newSets[m.ID] = next
		}
	}
	if err := e.cfg.Store.SetMessageFolders(newSets); err != nil {
		return err
	}
	e.changed(keys(groups)...)
	return e.perAccount(groups, "move messages", func(ctx context.Context, acct string, ms []models.MessageSummary) error {
		p, err := e.cfg.Provider(acct)
		if err != nil {
			return err
		}
		t := targets[acct]
		return p.Move(ctx, remoteIDs(ms), mail.Folder{RemoteID: t.RemoteID, Name: t.Name, Kind: t.Kind})
	}, func(acct string, ms []models.MessageSummary) {
		old := map[string][]string{}
		for _, m := range ms {
			old[m.ID] = current[m.ID]
		}
		_ = e.cfg.Store.SetMessageFolders(old)
	})
}

// PushLabel adds/removes a provider label on the server for accounts whose
// provider knows the label. Errors are logged; local state is authoritative.
func (e *Engine) PushLabel(ids []string, labelName string, add bool) {
	groups, err := e.group(ids)
	if err != nil {
		return
	}
	for acct, ms := range groups {
		if !e.HasProviderLabel(acct, labelName) {
			continue
		}
		e.wg.Add(1)
		go func(acct string, ms []models.MessageSummary) {
			defer e.wg.Done()
			defer safe.Recover("push label")
			ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
			defer cancel()
			p, err := e.cfg.Provider(acct)
			if err == nil {
				err = p.SetLabel(ctx, remoteIDs(ms), labelName, add)
			}
			if err != nil {
				slog.Warn("pushing label to provider failed", "account", acct, "label", labelName, "err", err)
			}
		}(acct, ms)
	}
}

// MarkFolderRead marks every message of a folder read (optimistically).
func (e *Engine) MarkFolderRead(folderID string) error {
	f, err := e.cfg.Store.Folder(folderID)
	if err != nil {
		return errors.New("that folder no longer exists")
	}
	ids, err := e.cfg.Store.ResolveIDs(models.ListQuery{FolderID: folderID, UnreadOnly: true}, store.FilterOptions{}, provider.MaxFolderOp)
	if err != nil {
		return err
	}
	changed, err := e.cfg.Store.SetUnread(ids, false)
	if err != nil {
		return err
	}
	_ = e.cfg.Store.SetFolderCounts(f.ID, 0, f.Total)
	e.changed(f.AccountID)
	p, err := e.cfg.Provider(f.AccountID)
	if err == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Minute)
		err = p.MarkFolderRead(ctx, mail.Folder{RemoteID: f.RemoteID, Name: f.Name, Kind: f.Kind})
		cancel()
	}
	if err != nil {
		_, _ = e.cfg.Store.SetUnread(changed, true)
		_ = e.cfg.Store.SetFolderCounts(f.ID, f.Unread, f.Total)
		e.changed(f.AccountID)
		a, _ := e.cfg.Store.Account(f.AccountID)
		return fmt.Errorf("couldn't mark %s as read: %s", f.Name, HumanError(a, err))
	}
	return nil
}

// EmptyFolder permanently deletes the contents of a Trash or Spam folder.
// Messages disappear locally right away and come back if the server
// refuses.
func (e *Engine) EmptyFolder(folderID string) error {
	f, err := e.cfg.Store.Folder(folderID)
	if err != nil {
		return errors.New("that folder no longer exists")
	}
	if f.Kind != models.FolderTrash && f.Kind != models.FolderSpam {
		return errors.New("only Trash and Spam can be emptied")
	}
	ids, err := e.cfg.Store.FolderMessageIDs(f.ID, false)
	if err != nil {
		return err
	}
	old, err := e.cfg.Store.MessageFolders(ids)
	if err != nil {
		return err
	}
	next := map[string][]string{}
	for _, id := range ids {
		var keep []string
		for _, fid := range old[id] {
			if fid != f.ID {
				keep = append(keep, fid)
			}
		}
		next[id] = keep
	}
	if err := e.cfg.Store.SetMessageFolders(next); err != nil {
		return err
	}
	_ = e.cfg.Store.SetFolderCounts(f.ID, 0, 0)
	e.changed(f.AccountID)
	p, err := e.cfg.Provider(f.AccountID)
	if err == nil {
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Minute)
		err = p.EmptyFolder(ctx, mail.Folder{RemoteID: f.RemoteID, Name: f.Name, Kind: f.Kind})
		cancel()
	}
	if err != nil {
		_ = e.cfg.Store.SetMessageFolders(old)
		_ = e.cfg.Store.SetFolderCounts(f.ID, f.Unread, f.Total)
		e.changed(f.AccountID)
		a, _ := e.cfg.Store.Account(f.AccountID)
		return fmt.Errorf("couldn't empty %s: %s", f.Name, HumanError(a, err))
	}
	// Drop local copies that are in no folder any more.
	sums, _ := e.cfg.Store.Summaries(ids)
	var gone []string
	for _, m := range sums {
		if len(m.FolderIDs) == 0 {
			gone = append(gone, m.RemoteID)
		}
	}
	if len(gone) > 0 {
		if _, err := e.cfg.Store.ApplySync(f.AccountID, mail.SyncResult{Deleted: gone}, nil); err != nil {
			slog.Warn("removing emptied messages", "err", err)
		}
	}
	e.changed(f.AccountID)
	return nil
}
