package product

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetTeamPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	c, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	respond(w, productcontrol.TeamPolicyConfiguration(c), err)
}

func (s *Service) PreviewTeamPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var policy api.ProductTeamPolicy
	if !operation.DecodeOperationBody(w, r, &policy, api.ProductControlMaxCatalogBytes) {
		return
	}
	c, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	var preview api.ProductTeamPolicyPreview
	if err == nil {
		preview, err = previewTeamPolicy(c, policy)
	}
	respond(w, preview, err)
}

func previewTeamPolicy(c api.ProductCatalog, policy api.ProductTeamPolicy) (api.ProductTeamPolicyPreview, error) {
	p, err := productcontrol.PreviewTeamPolicy(c, policy)
	if err != nil {
		return p, state.ErrInvalidArgument
	}
	return p, nil
}

func (s *Service) ApplyTeamPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var change api.ProductTeamPolicyChange
	if !operation.DecodeOperationBody(w, r, &change, api.ProductControlMaxCatalogBytes) {
		return
	}
	v, err := s.applyTeamPolicy(r, acct, app, store, change)
	accessRespond(w, v, err)
}

func (s *Service) applyTeamPolicy(r *http.Request, acct state.Account, app state.App, store state.ProductControlStore, change api.ProductTeamPolicyChange) (api.ProductTeamPolicyConfiguration, error) {
	if !accessRevision(change.ExpectedHead) || !accessRevision(change.ExpectedRevision) {
		return api.ProductTeamPolicyConfiguration{}, state.ErrInvalidArgument
	}
	base, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, change.ExpectedHead)
	if err != nil {
		return api.ProductTeamPolicyConfiguration{}, err
	}
	p, err := previewTeamPolicy(base, change.Policy)
	if err != nil || p.After.Revision != change.ExpectedRevision {
		return api.ProductTeamPolicyConfiguration{}, state.ErrInvalidArgument
	}
	spec := base.Spec
	spec.Team = &p.Change.Policy
	publisher, ok := s.Host.Store().(state.ProductSetupStore)
	if !ok {
		return api.ProductTeamPolicyConfiguration{}, state.ErrNotFound
	}
	c, err := publisher.PublishProductCatalog(r.Context(), acct.ID, app.ID, spec, change.ExpectedHead)
	if err != nil {
		return api.ProductTeamPolicyConfiguration{}, err
	}
	s.Host.EmitAudit(r.Context(), "product.team_policy.published", &acct.ID, map[string]any{"app_id": app.ID, "before_revision": change.ExpectedHead, "revision": c.Revision})
	return productcontrol.TeamPolicyConfiguration(c), nil
}
