package product

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) onboardingOwner(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductOnboardingStore, state.App, bool) {
	if p, ok := httpkit.PrincipalFrom(r); ok {
		if p.Key != nil && p.Key.PlatformTenantID != "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Owner access required", "use the product owner's session or API credential"))
			return nil, state.App{}, false
		}
		if p.Key == nil && !customerSameOrigin(w, r) {
			return nil, state.App{}, false
		}
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, app, false
	}
	store, ok := s.Host.Store().(state.ProductOnboardingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Product Control onboarding storage is unavailable"))
	}
	return store, app, ok
}

func (s *Service) onboardingWorkspaces(ctx context.Context, realm customerRealm, token string) (api.ProductOnboardingWorkspaces, error) {
	result := api.ProductOnboardingWorkspaces{AuthApp: realm.authApp.Slug, AuthAppID: realm.authApp.ID, Workspaces: []api.ProductOnboardingWorkspace{}}
	if !validCookieToken(token) {
		return result, customerAuthFailure{http.StatusUnauthorized}
	}
	var reply struct {
		Organizations []api.ProductOnboardingWorkspace `json:"organizations"`
	}
	if err := s.authCall(ctx, realm, http.MethodGet, "/authz/v1/orgs", token, nil, &reply); err != nil {
		return result, err
	}
	if reply.Organizations == nil || len(reply.Organizations) > api.ProductControlAuthMaxOrganizations {
		return result, customerAuthFailure{http.StatusServiceUnavailable}
	}
	seen := map[string]bool{}
	for _, org := range reply.Organizations {
		id, err := uuid.Parse(org.ID)
		if err != nil || id == uuid.Nil || id.String() != org.ID || seen[org.ID] || !productcontrol.ValidOnboardingText(org.Name, api.ProductControlOnboardingNameMaxBytes) || !productcontrol.ValidOnboardingText(org.Slug, api.ProductControlOnboardingNameMaxBytes) {
			return result, customerAuthFailure{http.StatusServiceUnavailable}
		}
		seen[org.ID] = true
		if org.Role == "owner" || org.Role == "admin" {
			result.Workspaces = append(result.Workspaces, org)
		}
	}
	sort.Slice(result.Workspaces, func(i, j int) bool { return result.Workspaces[i].Slug < result.Workspaces[j].Slug })
	return result, nil
}

func (s *Service) verifyOnboardingWorkspace(ctx context.Context, realm customerRealm, token string, req api.ProductOnboardingRequest) (api.ProductOnboardingRequest, error) {
	list, err := s.onboardingWorkspaces(ctx, realm, token)
	if err != nil {
		return req, err
	}
	if (req.AuthApp != "" && req.AuthApp != list.AuthApp) || (req.AuthAppID != "" && !sameOnboardingID(req.AuthAppID, list.AuthAppID)) {
		return req, state.ErrProductOnboardingStale
	}
	for _, org := range list.Workspaces {
		if org.ID != req.OrganizationID {
			continue
		}
		if req.OrganizationName != "" && req.OrganizationName != org.Name {
			return req, state.ErrProductOnboardingStale
		}
		req.AuthApp, req.AuthAppID, req.OrganizationName = list.AuthApp, list.AuthAppID, org.Name
		return req, nil
	}
	return req, customerAuthFailure{http.StatusForbidden}
}

func sameOnboardingID(a, b string) bool {
	x, e := uuid.Parse(a)
	y, f := uuid.Parse(b)
	return e == nil && f == nil && x != uuid.Nil && x == y
}

func (s *Service) OnboardingWorkspaces(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, _, ok := s.onboardingOwner(w, r, acct); !ok {
		return
	}
	var body api.ProductOnboardingAuthRequest
	if !operation.DecodeOperationBody(w, r, &body, api.ProductControlOnboardingBodyMaxBytes) {
		return
	}
	realm, err := s.customerRealm(r)
	var list api.ProductOnboardingWorkspaces
	if err == nil {
		list, err = s.onboardingWorkspaces(r.Context(), realm, body.AuthAccessToken)
	}
	onboardingRespond(w, list, err)
}

func (s *Service) OnboardingPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.onboardingOwner(w, r, acct)
	if !ok {
		return
	}
	var body api.ProductOnboardingReviewRequest
	if !operation.DecodeOperationBody(w, r, &body, api.ProductControlOnboardingBodyMaxBytes) {
		return
	}
	realm, err := s.customerRealm(r)
	var preview api.ProductOnboardingPreview
	if err == nil {
		body.Request, err = s.verifyOnboardingWorkspace(r.Context(), realm, body.AuthAccessToken, body.Request)
	}
	if err == nil {
		preview, err = store.PreviewProductOnboarding(r.Context(), acct.ID, app.ID, body.Request)
	}
	onboardingRespond(w, preview, err)
}

func (s *Service) applyOnboarding(r *http.Request, acct state.Account, app state.App, store state.ProductOnboardingStore, c api.ProductOnboardingConfirmation, token string) (api.ProductOnboardingReceipt, error) {
	// Recover a retained receipt before requiring a fresh Auth session. This is
	// read recovery, never permission to reapply a previous assignment.
	old, err := store.GetProductOnboarding(r.Context(), acct.ID, app.ID, c.Request.ID)
	if err == nil {
		if old.Receipt.Request != c.Request || old.Receipt.PreviewHash != c.ExpectedHash {
			return api.ProductOnboardingReceipt{}, state.ErrConflict
		}
		return old.Receipt, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return api.ProductOnboardingReceipt{}, err
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	verified, err := s.verifyOnboardingWorkspace(r.Context(), realm, token, c.Request)
	if err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	if verified != c.Request {
		return api.ProductOnboardingReceipt{}, state.ErrProductOnboardingStale
	}
	return store.ApplyProductOnboarding(r.Context(), acct.ID, app.ID, c)
}

func (s *Service) OnboardingConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.onboardingOwner(w, r, acct)
	if !ok {
		return
	}
	var body api.ProductOnboardingApplyRequest
	if !operation.DecodeOperationBody(w, r, &body, api.ProductControlOnboardingBodyMaxBytes) {
		return
	}
	receipt, err := s.applyOnboarding(r, acct, app, store, body.Confirmation, body.AuthAccessToken)
	onboardingRespond(w, receipt, err)
}

func (s *Service) OnboardingStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.onboardingOwner(w, r, acct)
	if !ok {
		return
	}
	status, err := store.GetProductOnboarding(r.Context(), acct.ID, app.ID, r.PathValue("onboarding"))
	if err == nil {
		s.onboardingLinks(r, acct, app, &status)
	}
	onboardingRespond(w, status, err)
}

func (s *Service) onboardingLinks(r *http.Request, acct state.Account, app state.App, status *api.ProductOnboardingStatus) {
	status.PortalPath = "/v1/product-control/portal?app=" + url.QueryEscape(app.Slug)
	if s.AuthOrigin != nil {
		a, origin, err := s.AuthOrigin(r.Context(), acct, status.Receipt.Request.AuthApp)
		if err == nil && sameOnboardingID(a.ID, status.Receipt.Request.AuthAppID) {
			status.AuthSignInURL = origin + "/ui/sign-in"
		}
	}
}

func onboardingRespond(w http.ResponseWriter, value any, err error) {
	var auth customerAuthFailure
	var quota *state.PlatformTenantQuotaError
	switch {
	case errors.Is(err, state.ErrProductOnboardingStale):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_onboarding_changed", "Onboarding review changed", "review the current customer, workspace, catalog and UTC month again"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_quota", "Customer limit reached", "this account has reached its platform tenant limit"))
	case errors.As(err, &auth):
		customerError(w, err)
	default:
		respond(w, value, err)
	}
}
