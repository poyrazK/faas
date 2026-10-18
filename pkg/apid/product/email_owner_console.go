package product

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) OwnerEmail(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "status", "cursor")
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	var page api.ProductEmailOwnerPage
	if err == nil {
		page, err = s.ownerEmailPage(ctx, acct, c.app, q.Get("tenant"), helpStatusFilter(q.Get("status"), "all"), q.Get("cursor"))
	}
	if err != nil {
		s.ownerEmailConsoleError(w, r, c, err)
		return
	}
	v := &dashboard.ProductControlEmail{Report: page}
	if page.OldestPendingAt != nil {
		v.BacklogAge = max(time.Duration(0), page.ObservedAt.Sub(*page.OldestPendingAt)).Truncate(time.Second).String()
	}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/email?" + url.Values{"tenant": {page.TenantID}, "status": {page.Status}, "cursor": {page.NextCursor}}.Encode()
	}
	for _, d := range page.Deliveries {
		row := dashboard.ProductControlEmailRow{ProductEmailOwnerDelivery: d, StatusLabel: productemail.StatusLabel(d.Status), ReasonLabel: productemail.ReasonLabel(d.Reason), RetryLabel: productemail.RetryLabel(d)}
		if d.RetryEligible {
			if v.Token == "" {
				v.Token, err = s.ownerToken(w, c, "email_retry", acct.ID+"/"+c.app.ID)
			}
			row.Token = v.Token
			if err != nil {
				break
			}
		}
		v.Deliveries = append(v.Deliveries, row)
	}
	if err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	c.data.Email = v
	s.OwnerRender(w, r, acct, c.data)
}
func (s *Service) OwnerEmailRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "tenant", "attempt"})
	var attempt int
	if err == nil {
		attempt, err = strconv.Atoi(f.Get("attempt"))
		if err != nil || strconv.Itoa(attempt) != f.Get("attempt") {
			err = state.ErrInvalidArgument
		}
	}
	id := r.PathValue("delivery")
	if err == nil && (!ownerUUID(id) || !ownerUUID(f.Get("tenant"))) {
		err = state.ErrInvalidArgument
	}
	if err == nil {
		err = s.ownerVerify(r, c, "email_retry", acct.ID+"/"+c.app.ID)
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	receipt, err := s.retryOwnerEmail(ctx, acct, c.app, id, api.ProductEmailRetryRequest{TenantID: f.Get("tenant"), ExpectedAttempts: &attempt})
	if err != nil {
		s.ownerEmailConsoleError(w, r, c, err)
		return
	}
	c.data.Notice = "Retry queued at " + receipt.QueuedAt.UTC().Format(time.RFC3339) + ". The worker checks current access before sending. Repeating this action returns the same receipt."
	c.data.EmailRetry = &receipt
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) ownerEmailConsoleError(w http.ResponseWriter, r *http.Request, c *ownerContext, err error) {
	if errors.Is(err, productemail.ErrConflict) {
		c.data.Error = "This message is no longer eligible for retry. Refresh email history and check current consent, workspace access, source updates and the remaining attempt budget."
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		s.OwnerRender(w, r, c.account, c.data)
		return
	}
	s.ownerError(w, r, c, ownerEmailError(err))
}
