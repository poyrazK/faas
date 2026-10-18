package product

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CustomerEmailOptOuts(w http.ResponseWriter, r *http.Request, _ state.Account) {
	q, err := ownerQuery(r, "cursor")
	if err != nil {
		emailRespond(w, nil, err)
		return
	}
	c, err := s.emailCustomer(r)
	if err != nil {
		customerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	scope := productemail.OwnerScope{AccountID: c.key.AccountID, AppID: c.key.AppID, TenantID: c.key.TenantID}
	value, err := c.store.OptOuts(ctx, scope, c.key.ID(), q.Get("cursor"), time.Now().UTC())
	emailRespond(w, value, err)
}
func (s *Service) EmailOptOuts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductEmailOptOutPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		page, err = s.ownerEmailOptOuts(ctx, acct, app, q.Get("tenant"), q.Get("cursor"))
	}
	emailRespond(w, page, err)
}
func (s *Service) ownerEmailOptOuts(ctx context.Context, acct state.Account, app state.App, tenant, cursor string) (api.ProductEmailOptOutPage, error) {
	var page api.ProductEmailOptOutPage
	scope, err := s.ownerEmailScope(ctx, acct, app, tenant)
	if err != nil {
		return page, err
	}
	repo, err := s.ownerEmailRepository()
	if err != nil {
		return page, err
	}
	return repo.OptOuts(ctx, scope, "", cursor, time.Now().UTC())
}
func (s *Service) OwnerEmailOptOuts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductEmailOptOutPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		page, err = s.ownerEmailOptOuts(ctx, acct, c.app, q.Get("tenant"), q.Get("cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, ownerEmailError(err))
		return
	}
	v := &dashboard.ProductControlEmailOptOuts{Page: page}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/email-opt-outs?" + url.Values{"tenant": {page.TenantID}, "cursor": {page.NextCursor}}.Encode()
	}
	c.data.EmailOptOuts = v
	s.OwnerRender(w, r, acct, c.data)
}
