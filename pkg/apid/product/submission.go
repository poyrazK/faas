package product

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) LookupSubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	var req struct {
		App     string `json:"app"`
		Feature string `json:"feature"`
		Key     string `json:"idempotency_key"`
	}
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	if slug := r.PathValue("slug"); slug != "" {
		if req.App != "" && req.App != slug {
			respond(w, nil, state.ErrNotFound)
			return
		}
		req.App = slug
	}
	r.SetPathValue("slug", req.App)
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	observation, err := store.LookupProductSubmission(r.Context(), state.ProductFeatureRequest{AccountID: acct.ID, AppID: app.ID, TenantID: tenant, Feature: req.Feature, IdempotencyKey: req.Key})
	if identity, ok := authmw.ProductCustomerFromContext(r.Context()); ok && observation.Receipt != nil {
		path := "/v1/product-control/customer/" + url.PathEscape(identity.ProductSlug) + "/operations/" + observation.Receipt.ID
		observation.Receipt.StatusURL, observation.Receipt.EventsURL = path, path+"/events"
	}
	respond(w, observation, err)
}
