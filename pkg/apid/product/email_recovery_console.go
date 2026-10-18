package product

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) OwnerEmailRecoveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "cursor")
	var page api.ProductEmailRecoveryPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
		defer cancel()
		page, err = s.ownerEmailRecoveries(ctx, acct, c.app, q.Get("tenant"), q.Get("cursor"))
	}
	if err != nil {
		s.ownerError(w, r, c, ownerEmailError(err))
		return
	}
	v := &dashboard.ProductControlEmailRecoveries{Page: page}
	if page.NextCursor != "" {
		v.NextURL = c.data.BaseURL + "/email-recoveries?" + url.Values{"tenant": {page.TenantID}, "cursor": {page.NextCursor}}.Encode()
	}
	c.data.EmailRecoveries = v
	s.OwnerRender(w, r, acct, c.data)
}
