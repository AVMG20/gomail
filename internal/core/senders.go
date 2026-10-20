package core

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// "Clean up by sender": who fills a mailbox's inbox, biggest first, so the
// UI can junk, label or archive a sender's mail in one go.

// Limits of TopSenders.
const (
	defaultTopSenders = 200
	maxTopSenders     = 2000
)

// TopSenders lists the senders of a mailbox's folder kind ("" = inbox,
// "all" = received mail outside Junk and Trash), the most mail first.
// groupBy "domain" puts every address at a company domain in one row;
// shared mail services (gmail.com, …) and the mailbox's own domain stay
// per address, since a rule for them would catch unrelated people.
// accountID "" covers every mailbox (one row per mailbox and sender).
func (s *Service) TopSenders(accountID, kind, groupBy string, limit int) ([]models.SenderSummary, error) {
	if limit <= 0 {
		limit = defaultTopSenders
	}
	limit = min(limit, maxTopSenders)
	if groupBy != "" && groupBy != "address" && groupBy != "domain" {
		return nil, errors.New("group by address or domain")
	}
	stats, err := s.store.SenderStats(accountID, kind)
	if err != nil {
		return nil, err
	}
	own := map[string]string{} // account → own domain
	if groupBy == "domain" {
		accts, err := s.store.Accounts()
		if err != nil {
			return nil, err
		}
		for _, a := range accts {
			if at := strings.LastIndex(a.Email, "@"); at >= 0 {
				own[a.ID] = strings.ToLower(a.Email[at+1:])
			}
		}
	}

	type group struct {
		sum    models.SenderSummary
		newest int64
		rid    int64
	}
	groups := map[string]*group{}
	order := []*group{}
	for _, st := range stats {
		domain := ""
		if at := strings.LastIndex(st.Address, "@"); at >= 0 {
			domain = st.Address[at+1:]
		}
		key := st.Address
		if groupBy == "domain" && domain != "" && !IsFreeMailDomain(domain) && domain != own[st.AccountID] {
			key = "@" + domain
		}
		g := groups[st.AccountID+"\n"+key]
		if g == nil {
			g = &group{sum: models.SenderSummary{AccountID: st.AccountID, Key: key, Domain: domain}}
			groups[st.AccountID+"\n"+key] = g
			order = append(order, g)
		}
		g.sum.Addresses++
		g.sum.Count += st.Count
		g.sum.Unread += st.Unread
		if g.rid == 0 || st.Newest > g.newest {
			g.newest, g.rid = st.Newest, st.NewestRid
			g.sum.Address, g.sum.Name = st.Address, st.Name
		}
	}
	slices.SortFunc(order, func(a, b *group) int {
		return cmp.Or(cmp.Compare(b.sum.Count, a.sum.Count), cmp.Compare(b.newest, a.newest),
			cmp.Compare(a.sum.Key, b.sum.Key), cmp.Compare(a.sum.AccountID, b.sum.AccountID))
	})
	order = order[:min(len(order), limit)]

	rids := make([]int64, len(order))
	for i, g := range order {
		rids[i] = g.rid
	}
	unsub, err := s.store.WithUnsubscribe(rids)
	if err != nil {
		return nil, err
	}
	out := make([]models.SenderSummary, len(order))
	for i, g := range order {
		g.sum.Newest = time.UnixMilli(g.newest)
		g.sum.Unsubscribe = unsub[g.rid]
		out[i] = g.sum
	}
	return out, nil
}

// SenderMessageIDs lists the mail of one sender (an address, or "@domain")
// in a mailbox's folder kind ("" = inbox), for "Archive all" in Clean up.
// It matches the address exactly, unlike a from: search.
func (s *Service) SenderMessageIDs(accountID, kind, sender string) ([]string, error) {
	return s.store.SenderMessageIDs(accountID, kind, sender)
}
