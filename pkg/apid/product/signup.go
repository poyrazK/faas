package product

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

// Self-serve signup (ADR-20261022100735): a visitor signs up in the owner's
// Application Auth app, then creates their own workspace on an offered plan.
// Gregale creates the Auth organization with the user's own session, the
// customer in the owner's billing provider, and runs the guided onboarding
// transaction (ADR-1707).

// errSignupUnavailable answers 404 for a product without signup, so the
// offer reveals nothing about products that do not offer it.
var errSignupUnavailable = fmt.Errorf("%w: signup is not offered", state.ErrNotFound)

func (s *Service) signupRealm(r *http.Request) (customerRealm, error) {
	realm, err := s.customerRealm(r)
	if err != nil {
		return realm, err
	}
	if s.SignupEnabled == nil || !s.SignupEnabled() || realm.catalog.Spec.Signup == nil || s.EmailOrigin == "" {
		return realm, errSignupUnavailable
	}
	return realm, nil
}

// portalURL is a product's customer portal on the platform's public origin,
// never on the request's Host.
func (s *Service) portalURL(slug string) string {
	return s.EmailOrigin + "/v1/product-control/portal?app=" + url.QueryEscape(slug)
}

// CustomerSignupOffer is the public offer: the offered plans' terms and the
// Auth app's sign-up page, returning to the portal.
func (s *Service) CustomerSignupOffer(w http.ResponseWriter, r *http.Request) {
	realm, err := s.signupRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	signUp, err := customerAuthPage(realm, "/ui/sign-up")
	if err != nil {
		customerError(w, err)
		return
	}
	signUp.RawQuery = url.Values{"redirect_to": {s.portalURL(realm.app.Slug)}}.Encode()
	policy := realm.catalog.Spec.Signup
	offer := api.ProductSignupOffer{Product: realm.app.Slug, Currency: realm.catalog.Spec.Currency, Provider: policy.Provider, Plans: []api.ProductPlanTerms{}, SignUpURL: signUp.String()}
	for _, plan := range policy.Plans {
		offer.Plans = append(offer.Plans, signupPlanTerms(realm.catalog, plan))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, offer)
}

// signupPlanTerms are a plan's public terms, without any customer's usage.
func signupPlanTerms(c api.ProductCatalog, name string) api.ProductPlanTerms {
	p := c.Spec.Plans[name]
	out := api.ProductPlanTerms{Plan: name, Label: p.Label, Revision: c.Revision, Currency: c.Spec.Currency, Features: []api.ProductPlanFeatureTerms{},
		MonthlyFeeMillicents: p.MonthlyFeeMillicents, TrialDays: p.TrialDays}
	for feature, grant := range p.Features {
		out.Features = append(out.Features, api.ProductPlanFeatureTerms{Name: feature, Label: c.Spec.Features[feature].Label, MonthlyStarts: grant.MonthlyStarts,
			UnitPriceMillicents: grant.UnitPriceMillicents, RemainingStarts: grant.MonthlyStarts, IncludedStarts: grant.IncludedStarts})
	}
	sort.Slice(out.Features, func(i, j int) bool { return out.Features[i].Name < out.Features[j].Name })
	return out
}

// CustomerSignup creates the signed-in user's workspace on an offered plan.
// A retry with the same id returns the workspace the first attempt made.
func (s *Service) CustomerSignup(w http.ResponseWriter, r *http.Request) {
	if !customerSameOrigin(w, r) {
		return
	}
	realm, err := s.signupRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	var req api.ProductSignupRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	req.WorkspaceName = strings.TrimSpace(req.WorkspaceName)
	if id, err := uuid.Parse(req.ID); err != nil || id == uuid.Nil || id.String() != req.ID || !productcontrol.SignupOffers(realm.catalog.Spec.Signup, req.Plan) ||
		!productcontrol.ValidOnboardingText(req.WorkspaceName, api.ProductControlOnboardingNameMaxBytes) {
		api.WriteProblem(w, api.ErrValidation("signup requires a request UUID, an offered plan and a workspace name of at most 128 bytes"))
		return
	}
	cookie, err := r.Cookie(realm.cookieName("access"))
	if err != nil || !validCookieToken(cookie.Value) {
		customerError(w, customerAuthFailure{http.StatusUnauthorized})
		return
	}
	receipt, problem, err := s.signup(r, realm, cookie.Value, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err != nil {
		onboardingRespond(w, nil, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ProductSignupReceipt{OrganizationID: receipt.Request.OrganizationID, Workspace: receipt.Request.OrganizationName, Customer: receipt.Customer})
}

func (s *Service) signup(r *http.Request, realm customerRealm, token string, req api.ProductSignupRequest) (api.ProductOnboardingReceipt, *api.Problem, error) {
	ctx := r.Context()
	onboarding, ok := s.Host.Store().(state.ProductOnboardingStore)
	if !ok {
		return api.ProductOnboardingReceipt{}, nil, state.ErrNotFound
	}
	// A retained receipt answers the same user's retry before any provider
	// call; another user's request ID reveals nothing.
	if old, err := onboarding.GetProductOnboarding(ctx, realm.account.ID, realm.app.ID, req.ID); err == nil {
		if _, err := s.verifyOnboardingWorkspace(ctx, realm, token, old.Receipt.Request); err != nil || old.Receipt.Request.Plan != req.Plan {
			return api.ProductOnboardingReceipt{}, nil, state.ErrConflict
		}
		return old.Receipt, nil, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return api.ProductOnboardingReceipt{}, nil, err
	}
	var user struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := s.authCall(ctx, realm, http.MethodGet, "/auth/v1/user", token, nil, &user); err != nil {
		return api.ProductOnboardingReceipt{}, nil, err
	}
	if problem, err := s.signupWithinLimit(r, realm); problem != nil || err != nil {
		return api.ProductOnboardingReceipt{}, problem, err
	}
	org, err := s.signupOrganization(ctx, realm, token, productcontrol.SignupOrganizationSlug(realm.app.ID, req.ID), req.WorkspaceName)
	if err != nil {
		return api.ProductOnboardingReceipt{}, nil, err
	}
	ref, problem, err := s.signupCustomerRef(ctx, realm, user.Email, req, org.ID)
	if problem != nil || err != nil {
		return api.ProductOnboardingReceipt{}, problem, err
	}
	request := api.ProductOnboardingRequest{ID: req.ID, ExternalRef: ref, Name: req.WorkspaceName, Plan: req.Plan, AuthApp: realm.authApp.Slug,
		AuthAppID: realm.authApp.ID, OrganizationID: org.ID, OrganizationName: org.Name}
	verified, err := s.verifyOnboardingWorkspace(ctx, realm, token, request)
	if err != nil {
		return api.ProductOnboardingReceipt{}, nil, err
	}
	preview, err := onboarding.PreviewProductOnboarding(ctx, realm.account.ID, realm.app.ID, verified)
	if err != nil {
		return api.ProductOnboardingReceipt{}, nil, err
	}
	receipt, err := onboarding.ApplyProductOnboarding(ctx, realm.account.ID, realm.app.ID, api.ProductOnboardingConfirmation{Request: preview.Request, ExpectedHash: preview.Hash})
	if err == nil {
		s.Host.EmitAudit(ctx, "product.customer.signed_up", &realm.account.ID, map[string]any{"app_id": realm.app.ID, "tenant_id": receipt.Customer.TenantID,
			"organization_id": org.ID, "plan": req.Plan, "provider": realm.catalog.Spec.Signup.Provider})
	}
	return receipt, nil, err
}

// signupCustomerRef is the new workspace's platform tenant reference: the
// customer the policy's provider created for it, else the organization.
func (s *Service) signupCustomerRef(ctx context.Context, realm customerRealm, email string, req api.ProductSignupRequest, org string) (string, *api.Problem, error) {
	provider := realm.catalog.Spec.Signup.Provider
	if provider == "" {
		return "signup:" + org, nil, nil
	}
	if s.SignupCustomer == nil || email == "" {
		return "", api.NewProblem(http.StatusConflict, api.CodeProductSignupUnavailable, "Signup unavailable", "the product's billing provider is unavailable"), nil
	}
	ref, problem := s.SignupCustomer(ctx, realm.account, provider, "gregale-signup-"+realm.app.ID+"-"+req.ID, email, req.WorkspaceName,
		map[string]string{"gregale_app": realm.app.Slug, "gregale_organization": org})
	if problem != nil {
		return "", problem, nil
	}
	// Paddle keeps one customer per email, so a second signup can name a
	// customer another workspace already bills; it never takes that over.
	resolver, ok := s.Host.Store().(state.PlatformTenantExternalRefResolver)
	if !ok {
		return "", nil, customerAuthFailure{http.StatusServiceUnavailable}
	}
	if _, err := resolver.ResolvePlatformTenantExternalRef(ctx, realm.account.ID, ref); err == nil {
		return "", api.NewProblem(http.StatusConflict, api.CodeProductSignupLimit, "Workspace exists",
			"your email already has a workspace billed by this product team; sign in to it or use another email"), nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return "", nil, err
	}
	return ref, nil, nil
}

// signupWithinLimit refuses a user who already owns the most workspaces a
// user may create in the product.
func (s *Service) signupWithinLimit(r *http.Request, realm customerRealm) (*api.Problem, error) {
	workspaces, err := s.customerWorkspaces(r, realm)
	if err != nil {
		return nil, err
	}
	owned := 0
	for _, w := range workspaces {
		if w.Access.Role == "owner" {
			owned++
		}
	}
	if owned >= api.ProductControlSignupMaxWorkspacesPerUser {
		return api.NewProblem(http.StatusConflict, api.CodeProductSignupLimit, "Workspace limit reached",
			fmt.Sprintf("a user may create at most %d workspaces in this product", api.ProductControlSignupMaxWorkspacesPerUser)), nil
	}
	return nil, nil
}

// signupOrganization returns the user's organization with slug, creating it
// with the user's own session when the first attempt did not.
func (s *Service) signupOrganization(ctx context.Context, realm customerRealm, token, slug, name string) (api.ProductOnboardingWorkspace, error) {
	find := func() (api.ProductOnboardingWorkspace, bool, error) {
		list, err := s.onboardingWorkspaces(ctx, realm, token)
		for _, org := range list.Workspaces {
			if org.Slug == slug && org.Role == "owner" {
				return org, true, nil
			}
		}
		return api.ProductOnboardingWorkspace{}, false, err
	}
	if org, ok, err := find(); ok || err != nil {
		return org, err
	}
	var created api.ProductOnboardingWorkspace
	if err := s.authCall(ctx, realm, http.MethodPost, "/authz/v1/orgs", token, map[string]string{"slug": slug, "name": name}, &created); err != nil {
		return created, err
	}
	org, ok, err := find()
	if err == nil && !ok {
		err = customerAuthFailure{http.StatusServiceUnavailable}
	}
	return org, err
}
