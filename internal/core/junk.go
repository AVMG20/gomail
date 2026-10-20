package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// Sender rules from the mail itself: "Always send <sender> to Junk" adds a
// junk rule for the sender in that mailbox and moves the sender's mail to
// Junk now; "Always label <sender> as …" adds a label rule and labels the
// sender's mail now; "Not junk" moves mail back to the inbox and reports
// the junk rules that would keep sending the sender to Junk.

// rulesMu serializes the rule edits made from the mail (they read, change
// and save the settings).
var rulesMu sync.Mutex

// freeMailDomains are shared mail services: a rule for their whole domain
// would catch unrelated people, so only the address is offered. Keep in
// sync with frontend/src/lib/junk.ts.
var freeMailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"yahoo.com": true, "ymail.com": true, "rocketmail.com": true, "icloud.com": true, "me.com": true, "mac.com": true,
	"aol.com": true, "proton.me": true, "protonmail.com": true, "pm.me": true, "gmx.com": true, "gmx.net": true, "gmx.de": true,
	"mail.com": true, "zoho.com": true, "yandex.com": true, "yandex.ru": true, "mail.ru": true, "fastmail.com": true,
	"tutanota.com": true, "tuta.io": true, "hey.com": true, "web.de": true, "t-online.de": true, "orange.fr": true,
	"free.fr": true, "laposte.net": true, "libero.it": true, "qq.com": true, "163.com": true, "126.com": true,
	"ziggo.nl": true, "kpnmail.nl": true, "hetnet.nl": true, "planet.nl": true, "home.nl": true, "xs4all.nl": true,
	"telenet.be": true, "skynet.be": true,
}

// freeMailPrefixes catch the country variants (hotmail.co.uk, yahoo.fr, …).
var freeMailPrefixes = []string{"gmail.", "hotmail.", "outlook.", "live.", "yahoo.", "msn.", "windowslive."}

// IsFreeMailDomain reports whether domain is a shared mail service.
func IsFreeMailDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if freeMailDomains[domain] {
		return true
	}
	for _, p := range freeMailPrefixes {
		if strings.HasPrefix(domain, p) {
			return true
		}
	}
	return false
}

// RuleRef is a sender rule of a mailbox, as the UI offers to undo or stop it.
type RuleRef struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Contains  string `json:"contains"` // the address, or "@domain"
	Action    string `json:"action"`   // "junk" | "label"
	LabelID   string `json:"labelId"`  // for "label"
}

func ruleRef(r models.LabelRule) RuleRef {
	return RuleRef{ID: r.ID, AccountID: r.AccountID, Contains: r.Contains, Action: r.Action, LabelID: r.LabelID}
}

// JunkSenderResult tells the UI what "Always send to Junk" did (its toast
// and Undo).
type JunkSenderResult struct {
	Rule    RuleRef  `json:"rule"`    // the rule that junks the sender from now on
	Created bool     `json:"created"` // false: an existing rule already covered the sender
	Count   int      `json:"count"`   // messages moved to Junk
	Moved   []string `json:"moved"`   // inbox mail moved besides the given messages (Undo: back to the inbox)
	Unread  []string `json:"unread"`  // messages that were unread before (Undo: unread again)
}

// LabelSenderResult tells the UI what "Always label" did.
type LabelSenderResult struct {
	Rule    RuleRef  `json:"rule"`
	Created bool     `json:"created"` // false: an existing rule already labeled the sender
	Labeled []string `json:"labeled"` // mail that got the label now (Undo takes it off)
}

// NotJunkResult tells the UI what "Not junk" did.
type NotJunkResult struct {
	Count int       `json:"count"` // messages moved to the inbox
	Rules []RuleRef `json:"rules"` // junk rules that still send these senders to Junk
}

// JunkSender sends a sender's mail in one mailbox to Junk from now on and
// moves what is already there: the given messages (rows the user picked,
// conversation ids allowed) and the sender's inbox mail. Everything moved
// is marked read, like mail Jev or a rule junks. scope is "address" or
// "domain" (every address at the sender's domain).
func (s *Service) JunkSender(accountID, address, scope string, ids []string) (JunkSenderResult, error) {
	res := JunkSenderResult{Moved: []string{}, Unread: []string{}}
	needle, err := s.senderNeedle(accountID, address, scope)
	if err != nil {
		return res, err
	}
	rule, created, err := s.ensureSenderRule(accountID, needle, "junk", "")
	if err != nil {
		return res, err
	}
	res.Rule, res.Created = rule, created

	// The picked messages, then the sender's inbox mail the rule matches.
	if len(ids) > 0 {
		if ids, err = s.expand(ids, store.ExpandFolder); err != nil {
			return res, err
		}
	}
	given, err := s.store.RuleCandidates(ids)
	if err != nil {
		return res, err
	}
	inbox, err := s.store.SenderMail(accountID, needle, true)
	if err != nil {
		return res, err
	}
	match := models.LabelRule{AccountID: accountID, Field: "from", Contains: needle, Action: "junk"}
	picked := map[string]bool{}
	var all, move []string
	for _, c := range given {
		if c.AccountID == accountID && !picked[c.ID] {
			picked[c.ID] = true
			all = append(all, c.ID)
			if !c.InSpam {
				move = append(move, c.ID)
			}
		}
	}
	for _, c := range inbox {
		if picked[c.ID] || c.InSpam || !ruleMatches(match, c.AccountID, c.FromName, c.FromEmail, c.Subject) {
			continue
		}
		picked[c.ID] = true
		all = append(all, c.ID)
		move = append(move, c.ID)
		res.Moved = append(res.Moved, c.ID)
	}
	if len(all) == 0 {
		return res, nil
	}
	if res.Unread, err = s.unreadOf(all); err != nil {
		return res, err
	}
	if res.Unread == nil {
		res.Unread = []string{}
	}
	moved, moveErr := s.junkRead(res.Unread, move, "junk sender")
	res.Count = len(moved)
	s.messagesChanged(all)
	return res, moveErr
}

// LabelSender labels a sender's mail in one mailbox from now on (a label
// rule) and labels the sender's received mail already there. scope is
// "address" or "domain".
func (s *Service) LabelSender(accountID, address, scope, labelID string) (LabelSenderResult, error) {
	res := LabelSenderResult{Labeled: []string{}}
	l, err := s.label(labelID)
	if err != nil {
		return res, err
	}
	if l.ID == config.JunkLabelID {
		return res, errors.New("use “Always send to Junk” for Junk")
	}
	if !config.LabelInAccount(l, accountID) {
		return res, fmt.Errorf("the label “%s” isn’t used in this mailbox", l.Name)
	}
	needle, err := s.senderNeedle(accountID, address, scope)
	if err != nil {
		return res, err
	}
	rule, created, err := s.ensureSenderRule(accountID, needle, "label", labelID)
	if err != nil {
		return res, err
	}
	res.Rule, res.Created = rule, created
	cands, err := s.store.SenderMail(accountID, needle, false)
	if err != nil {
		return res, err
	}
	match := models.LabelRule{AccountID: accountID, Field: "from", Contains: needle, Action: "label", LabelID: labelID}
	var ids []string
	for _, c := range cands {
		if ruleMatches(match, c.AccountID, c.FromName, c.FromEmail, c.Subject) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return res, nil
	}
	added, err := s.store.ApplyRuleLabel(ids, labelID)
	if err != nil {
		return res, err
	}
	if len(added) > 0 {
		res.Labeled = added
		if l.Source == config.LabelSourceProvider {
			for _, b := range batches(added, ruleBatch) {
				s.engine.PushLabel(b, l.Name, true)
			}
		}
		s.messagesChanged(added)
	}
	return res, nil
}

// senderNeedle is the text a sender rule matches: the address, or
// "@domain" for scope "domain" (refused for shared mail services and the
// mailbox's own domain, which would catch unrelated people).
func (s *Service) senderNeedle(accountID, address, scope string) (string, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	at := strings.LastIndex(address, "@")
	if at <= 0 || at == len(address)-1 {
		return "", errors.New("this sender has no valid address")
	}
	acct, err := s.store.Account(accountID)
	if err != nil {
		return "", errors.New("that mailbox no longer exists")
	}
	switch scope {
	case "address":
		return address, nil
	case "domain":
		domain := address[at+1:]
		if IsFreeMailDomain(domain) || strings.HasSuffix(strings.ToLower(acct.Email), "@"+domain) {
			return "", fmt.Errorf("%s is shared by many people; use the address instead", domain)
		}
		return "@" + domain, nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

// ensureSenderRule returns the mailbox's rule with this action (and label)
// that covers needle (an address or "@domain"), adding one when there is
// none. Rules are saved through SaveSettings, so they are normalized.
func (s *Service) ensureSenderRule(accountID, needle, action, labelID string) (RuleRef, bool, error) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	st := s.Settings()
	for _, r := range st.LabelRules {
		// A rule whose text is part of needle matches every sender needle
		// matches (e.g. "@example.com" covers alex@example.com).
		if r.Action == action && r.LabelID == labelID && r.Field == "from" && (r.AccountID == accountID || r.AccountID == "") &&
			r.Contains != "" && strings.Contains(needle, strings.ToLower(r.Contains)) {
			return ruleRef(r), false, nil
		}
	}
	r := models.LabelRule{ID: randomID("rule_"), AccountID: accountID, Field: "from", Contains: needle, Action: action, LabelID: labelID}
	st.LabelRules = append(slices.Clone(st.LabelRules), r)
	if err := s.SaveSettings(st); err != nil {
		return RuleRef{}, false, err
	}
	if !slices.ContainsFunc(s.Settings().LabelRules, func(x models.LabelRule) bool { return x.ID == r.ID }) {
		return RuleRef{}, false, errors.New("there are too many rules; remove some in Settings → Labels & rules")
	}
	return ruleRef(r), true, nil
}

// RemoveRules deletes rules by id ("Stop junking", a sender rule toggled
// off, Undo). Unknown ids are ignored; what the rules already did stays.
func (s *Service) RemoveRules(ruleIDs []string) error {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	st := s.Settings()
	n := len(st.LabelRules)
	st.LabelRules = slices.DeleteFunc(slices.Clone(st.LabelRules), func(r models.LabelRule) bool {
		return slices.Contains(ruleIDs, r.ID)
	})
	if len(st.LabelRules) == n {
		return nil
	}
	return s.SaveSettings(st)
}

// NotJunk moves messages (conversation ids allowed) back to their own
// mailbox's inbox. The user decided they aren't junk: Jev won't label them
// Junk again. It returns the junk rules that would still send these
// senders' new mail to Junk, so the UI can offer to stop them.
func (s *Service) NotJunk(ids []string) (NotJunkResult, error) {
	res := NotJunkResult{Rules: []RuleRef{}}
	ids, err := s.expand(ids, store.ExpandFolder)
	if err != nil || len(ids) == 0 {
		return res, err
	}
	sums, err := s.store.Summaries(ids)
	if err != nil {
		return res, err
	}
	// Before the move: Outlook gives moved mail new ids.
	if err := s.store.RejectLabel(ids, config.JunkLabelID); err != nil {
		return res, err
	}
	if err := s.engine.MoveToKind(ids, models.FolderInbox); err != nil {
		s.messagesChanged(ids)
		return res, err
	}
	res.Count = len(ids)
	s.messagesChanged(ids)
	seen := map[string]bool{}
	rules := s.Settings().LabelRules
	for _, m := range sums {
		for _, r := range rules {
			if r.Action == "junk" && r.Field == "from" && !seen[r.ID] && ruleMatches(r, m.AccountID, m.From.Name, m.From.Email, m.Subject) {
				seen[r.ID] = true
				res.Rules = append(res.Rules, ruleRef(r))
			}
		}
	}
	return res, nil
}
