package product

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) GetPlanPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	c, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	respond(w, productcontrol.PlanPolicyConfiguration(c), err)
}

func (s *Service) PreviewPlanPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var policy api.ProductPlanPolicy
	if !operation.DecodeOperationBody(w, r, &policy, api.ProductControlMaxCatalogBytes) {
		return
	}
	c, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	var preview api.ProductPlanPolicyPreview
	if err == nil {
		preview, err = previewPlanPolicy(c, policy)
	}
	respond(w, preview, err)
}

func previewPlanPolicy(c api.ProductCatalog, policy api.ProductPlanPolicy) (api.ProductPlanPolicyPreview, error) {
	p, err := productcontrol.PreviewPlanPolicy(c, policy)
	if err != nil {
		return p, state.ErrInvalidArgument
	}
	return p, nil
}

func (s *Service) ApplyPlanPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var change api.ProductPlanPolicyChange
	if !operation.DecodeOperationBody(w, r, &change, api.ProductControlMaxCatalogBytes) {
		return
	}
	v, err := s.applyPlanPolicy(r, acct, app, store, change)
	accessRespond(w, v, err)
}

func (s *Service) applyPlanPolicy(r *http.Request, acct state.Account, app state.App, store state.ProductControlStore, change api.ProductPlanPolicyChange) (api.ProductPlanPolicyConfiguration, error) {
	if !accessRevision(change.ExpectedHead) || !accessRevision(change.ExpectedRevision) {
		return api.ProductPlanPolicyConfiguration{}, state.ErrInvalidArgument
	}
	base, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, change.ExpectedHead)
	if err != nil {
		return api.ProductPlanPolicyConfiguration{}, err
	}
	p, err := previewPlanPolicy(base, change.Policy)
	if err != nil || p.After.Revision != change.ExpectedRevision {
		return api.ProductPlanPolicyConfiguration{}, state.ErrInvalidArgument
	}
	spec := base.Spec
	spec.SelfService = &p.Change.Policy
	publisher, ok := s.Host.Store().(state.ProductSetupStore)
	if !ok {
		return api.ProductPlanPolicyConfiguration{}, state.ErrNotFound
	}
	c, err := publisher.PublishProductCatalog(r.Context(), acct.ID, app.ID, spec, change.ExpectedHead)
	if err != nil {
		return api.ProductPlanPolicyConfiguration{}, err
	}
	s.Host.EmitAudit(r.Context(), "product.plan_policy.published", &acct.ID, map[string]any{"app_id": app.ID, "before_revision": change.ExpectedHead, "revision": c.Revision})
	return productcontrol.PlanPolicyConfiguration(c), nil
}
