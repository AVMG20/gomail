package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/netguard"
	"github.com/AVMG20/gomail/internal/syncer"
)

// unsubscribeClient performs RFC 8058 one-click POSTs: https only, no
// cookies, public addresses only (no requests into the local network),
// bounded redirects that must stay on https.
var unsubscribeClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: netguard.Transport(),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("refusing a non-https redirect")
		}
		return nil
	},
}

// Unsubscribe uses the message's List-Unsubscribe header: a one-click POST
// when offered, otherwise an email to the mailto address, otherwise the
// link is opened in the browser.
func (s *Service) Unsubscribe(messageID string) (models.UnsubscribeResult, error) {
	m, err := s.messageRaw(messageID)
	if err != nil {
		return models.UnsubscribeResult{}, err
	}
	u := m.Unsubscribe
	if u == nil {
		return models.UnsubscribeResult{}, errors.New("this sender doesn't offer an unsubscribe option")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if u.OneClick {
		if err := oneClick(ctx, u.URL); err == nil {
			return models.UnsubscribeResult{Method: "oneClick", URL: u.URL}, nil
		} else if u.Mailto == "" {
			// Fall back to the page, where the user can confirm manually.
			if oerr := s.OpenExternalURL(u.URL); oerr != nil {
				return models.UnsubscribeResult{}, fmt.Errorf("unsubscribing failed: %v", err)
			}
			return models.UnsubscribeResult{Method: "url", URL: u.URL}, nil
		}
	}
	if u.Mailto != "" {
		if err := s.sendUnsubscribeMail(ctx, m, u.Mailto); err != nil {
			return models.UnsubscribeResult{}, err
		}
		return models.UnsubscribeResult{Method: "mailto", URL: u.Mailto}, nil
	}
	if err := s.OpenExternalURL(u.URL); err != nil {
		return models.UnsubscribeResult{}, err
	}
	return models.UnsubscribeResult{Method: "url", URL: u.URL}, nil
}

func oneClick(ctx context.Context, link string) error {
	pu, err := url.Parse(link)
	if err != nil || pu.Scheme != "https" || pu.Host == "" {
		return errors.New("the unsubscribe link is not a secure https link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, link, strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "gomail")
	resp, err := unsubscribeClient.Do(req)
	if errors.Is(err, netguard.ErrBlocked) {
		return errors.New("the unsubscribe link points to a local network address, so gomail didn't follow it")
	}
	if err != nil {
		return fmt.Errorf("couldn't reach the sender's unsubscribe service: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the sender's unsubscribe service answered HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) sendUnsubscribeMail(ctx context.Context, m models.Message, mailto string) error {
	pu, err := url.Parse(mailto)
	if err != nil || pu.Scheme != "mailto" {
		return errors.New("the unsubscribe address is invalid")
	}
	addr := pu.Opaque
	if addr == "" {
		addr = pu.Path
	}
	if addr, err = url.PathUnescape(addr); err != nil || !strings.Contains(addr, "@") {
		return errors.New("the unsubscribe address is invalid")
	}
	q := pu.Query()
	subject, body := q.Get("subject"), q.Get("body")
	if subject == "" {
		subject = "unsubscribe"
	}
	if body == "" {
		body = "unsubscribe"
	}
	acct, err := s.store.Account(m.AccountID)
	if err != nil {
		return err
	}
	p, err := s.provider(acct.ID)
	if err != nil {
		return err
	}
	out := mail.Outgoing{From: models.Address{Name: acct.DisplayName, Email: acct.Email},
		To: []models.Address{{Email: addr}}, Subject: subject, Text: body, Mode: "new"}
	if err := p.Send(ctx, out); err != nil {
		return fmt.Errorf("sending the unsubscribe email failed: %s", syncer.HumanError(acct, err))
	}
	return nil
}
