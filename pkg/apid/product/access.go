package product

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) accessOwner(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductControlStore, state.App, bool) {
	if p, ok := httpkit.PrincipalFrom(r); ok {
		if p.Key != nil && p.Key.PlatformTenantID != "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Owner access required", "use the product owner's session or API credential"))
			return nil, state.App{}, false
		}
		if p.Key == nil && !customerSameOrigin(w, r) {
			return nil, state.App{}, false
		}
	}
	return s.owned(w, r, acct)
}

func (s *Service) GetAccess(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	catalog, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	respond(w, productcontrol.AccessConfiguration(catalog), err)
}

func (s *Service) PreviewAccess(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var policy api.ProductAccessPolicy
	if !operation.DecodeOperationBody(w, r, &policy, api.ProductControlMaxCatalogBytes) {
		return
	}
	catalog, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, "")
	var preview api.ProductAccessChangePreview
	if err == nil {
		preview, err = previewAccess(catalog, policy)
	}
	respond(w, preview, err)
}

func previewAccess(catalog api.ProductCatalog, policy api.ProductAccessPolicy) (api.ProductAccessChangePreview, error) {
	spec := catalog.Spec
	spec.Access = &policy
	raw, err := json.Marshal(spec)
	if err != nil || len(raw) > api.ProductControlMaxCatalogBytes {
		return api.ProductAccessChangePreview{}, state.ErrInvalidArgument
	}
	preview, err := productcontrol.PreviewAccess(catalog, policy)
	if err != nil {
		return api.ProductAccessChangePreview{}, state.ErrInvalidArgument
	}
	return preview, nil
}

func (s *Service) ApplyAccess(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.accessOwner(w, r, acct)
	if !ok {
		return
	}
	var change api.ProductAccessChangeRequest
	if !operation.DecodeOperationBody(w, r, &change, api.ProductControlMaxCatalogBytes) {
		return
	}
	value, err := s.applyAccess(r, acct, app, store, change)
	accessRespond(w, value, err)
}

func (s *Service) applyAccess(r *http.Request, acct state.Account, app state.App, store state.ProductControlStore, change api.ProductAccessChangeRequest) (api.ProductAccessConfiguration, error) {
	if !accessRevision(change.ExpectedHead) || !accessRevision(change.ExpectedRevision) || productcontrol.ValidateAccess(&change.Policy) != nil {
		return api.ProductAccessConfiguration{}, state.ErrInvalidArgument
	}
	// Rebuild from the reviewed immutable catalog, never from a newer head.
	before, err := store.GetProductCatalog(r.Context(), acct.ID, app.ID, change.ExpectedHead)
	if err != nil {
		return api.ProductAccessConfiguration{}, err
	}
	preview, err := previewAccess(before, change.Policy)
	if err != nil || preview.After.Revision != change.ExpectedRevision {
		return api.ProductAccessConfiguration{}, state.ErrInvalidArgument
	}
	spec := before.Spec
	spec.Access = &preview.Change.Policy
	publisher, ok := s.Host.Store().(state.ProductSetupStore)
	if !ok {
		return api.ProductAccessConfiguration{}, state.ErrNotFound
	}
	current, err := publisher.PublishProductCatalog(r.Context(), acct.ID, app.ID, spec, change.ExpectedHead)
	if err != nil {
		return api.ProductAccessConfiguration{}, err
	}
	s.Host.EmitAudit(r.Context(), "product.access.published", &acct.ID, map[string]any{"app_id": app.ID, "before_revision": change.ExpectedHead, "revision": current.Revision})
	return productcontrol.AccessConfiguration(current), nil
}

func accessRevision(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}

func accessRespond(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, state.ErrProductCatalogStale) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_access_review_changed", "Product access changed", "review the latest product configuration before changing permissions"))
		return
	}
	respond(w, value, err)
}
