package product

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) OwnerOnboarding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "id", "tenant", "tenant_cursor")
	if err == nil {
		err = s.loadOwnerOnboarding(w, r, c, q)
	}
	if err != nil {
		s.ownerOnboardingError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) loadOwnerOnboarding(w http.ResponseWriter, r *http.Request, c *ownerContext, q url.Values) error {
	store, ok := s.Host.Store().(state.ProductOnboardingStore)
	if !ok {
		return state.ErrNotFound
	}
	if q.Get("id") != "" {
		status, err := store.GetProductOnboarding(r.Context(), c.account.ID, c.app.ID, q.Get("id"))
		if err != nil {
			return err
		}
		s.onboardingLinks(r, c.account, c.app, &status)
		c.data.OnboardingStatus = &status
		return nil
	}
	if q.Get("tenant") != "" {
		status, err := store.ProductOnboardingByTenant(r.Context(), c.account.ID, c.app.ID, q.Get("tenant"))
		if err == nil {
			s.onboardingLinks(r, c.account, c.app, &status)
			c.data.OnboardingStatus = &status
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return err
	}
	catalog, err := c.products.GetProductCatalog(r.Context(), c.account.ID, c.app.ID, "")
	if err != nil {
		return err
	}
	f := &dashboard.ProductControlOnboarding{ID: uuid.NewString(), TenantID: q.Get("tenant"), AuthSignInURL: realm.origin + "/ui/sign-in"}
	c.data.Onboarding = f
	for name, p := range catalog.Spec.Plans {
		c.data.Plans = append(c.data.Plans, dashboard.ProductControlPlan{Name: name, Label: p.Label})
	}
	sort.Slice(c.data.Plans, func(i, j int) bool { return c.data.Plans[i].Name < c.data.Plans[j].Name })
	if err = s.onboardingOwnerTenants(r, c, q.Get("tenant_cursor")); err != nil {
		return err
	}
	if cookie, e := r.Cookie(onboardingOwnerCookie(c, realm.authApp.ID)); e == nil {
		list, e := s.onboardingWorkspaces(r.Context(), realm, cookie.Value)
		if e == nil {
			f.Workspaces, f.Connected = list.Workspaces, true
		} else {
			var auth customerAuthFailure
			if !errors.As(e, &auth) || auth.status == http.StatusServiceUnavailable {
				return e
			}
			s.clearOnboardingOwnerCookie(w, c, realm.authApp.ID)
			c.data.Notice = "Sign in to Application Auth again to select a workspace."
		}
	}
	f.SessionToken, err = s.ownerToken(w, c, "onboarding_session", c.account.ID+"/"+c.app.ID)
	if err == nil {
		f.ReviewToken, err = s.ownerToken(w, c, "onboarding_review", c.account.ID+"/"+c.app.ID)
	}
	return err
}

func (s *Service) onboardingOwnerTenants(r *http.Request, c *ownerContext, cursor string) error {
	f := c.data.Onboarding
	rows, next, err := c.tenants.ListPlatformTenantsPage(r.Context(), c.account.ID, api.ProductControlOwnerPageSize, 0, cursor)
	if err != nil {
		return err
	}
	found := false
	for _, t := range rows {
		if t.Status != state.PlatformTenantActive {
			continue
		}
		f.Tenants = append(f.Tenants, dashboard.ProductControlCustomer{TenantID: t.ID, Name: t.Name})
		found = found || t.ID == f.TenantID
	}
	if f.TenantID != "" && !found {
		if !ownerUUID(f.TenantID) {
			return state.ErrInvalidArgument
		}
		t, e := c.tenants.GetPlatformTenant(r.Context(), c.account.ID, f.TenantID)
		if e != nil {
			return e
		}
		if t.Status != state.PlatformTenantActive {
			return state.ErrProductAccess
		}
		f.Tenants = append(f.Tenants, dashboard.ProductControlCustomer{TenantID: t.ID, Name: t.Name})
	}
	if next != "" {
		f.NextTenantsURL = c.data.BaseURL + "/onboarding?tenant_cursor=" + url.QueryEscape(next)
	}
	return nil
}

func onboardingOwnerCookie(c *ownerContext, authID string) string {
	return "__Secure-gregale-pc-onboard-" + productcontrolOwnerHash(c.account.ID + "/" + c.app.ID + "/" + authID)[:16]
}

func (s *Service) clearOnboardingOwnerCookie(w http.ResponseWriter, c *ownerContext, authID string) {
	http.SetCookie(w, &http.Cookie{Name: onboardingOwnerCookie(c, authID), Path: c.data.BaseURL + "/onboarding", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (s *Service) OwnerOnboardingSession(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "email", "password"})
	if err == nil {
		err = s.ownerVerify(r, c, "onboarding_session", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.connectOwnerWorkspace(w, r, c, f)
	}
	if err != nil {
		s.ownerOnboardingError(w, r, c, err)
		return
	}
	http.Redirect(w, r, c.data.BaseURL+"/onboarding", http.StatusSeeOther)
}

func (s *Service) connectOwnerWorkspace(w http.ResponseWriter, r *http.Request, c *ownerContext, f url.Values) error {
	if f.Get("email") == "" || f.Get("password") == "" {
		return state.ErrInvalidArgument
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return err
	}
	var session struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	err = s.authCall(r.Context(), realm, http.MethodPost, "/auth/v1/token?grant_type=password", "", map[string]string{"email": f.Get("email"), "password": f.Get("password")}, &session)
	if err != nil {
		return err
	}
	if !validCookieToken(session.AccessToken) || session.ExpiresIn <= 0 || session.ExpiresIn > api.ProductControlSessionMaxSeconds {
		return customerAuthFailure{http.StatusServiceUnavailable}
	}
	list, err := s.onboardingWorkspaces(r.Context(), realm, session.AccessToken)
	if err != nil {
		return err
	}
	if len(list.Workspaces) == 0 {
		return customerAuthFailure{http.StatusForbidden}
	}
	http.SetCookie(w, &http.Cookie{Name: onboardingOwnerCookie(c, realm.authApp.ID), Value: session.AccessToken, Path: c.data.BaseURL + "/onboarding", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: min(session.ExpiresIn, api.ProductControlOnboardingSessionSeconds)})
	return nil
}

func (s *Service) OwnerOnboardingDisconnect(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	_, err := ownerParseForm(w, r, []string{"csrf_token"})
	if err == nil {
		err = s.ownerVerify(r, c, "onboarding_session", acct.ID+"/"+c.app.ID)
	}
	if err != nil {
		s.ownerOnboardingError(w, r, c, err)
		return
	}
	// Clear every scoped connection cookie received at this onboarding path,
	// even if the selected Auth realm changed or became unavailable.
	for _, cookie := range r.Cookies() {
		if len(cookie.Name) > len("__Secure-gregale-pc-onboard-") && cookie.Name[:len("__Secure-gregale-pc-onboard-")] == "__Secure-gregale-pc-onboard-" {
			http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: c.data.BaseURL + "/onboarding", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		}
	}
	http.Redirect(w, r, c.data.BaseURL+"/onboarding", http.StatusSeeOther)
}

func (s *Service) OwnerOnboardingReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "id", "tenant", "external_ref", "name", "plan", "organization"})
	if err == nil {
		err = s.ownerVerify(r, c, "onboarding_review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.reviewOwnerOnboarding(w, r, c, f)
	}
	if err != nil {
		s.ownerOnboardingError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) reviewOwnerOnboarding(w http.ResponseWriter, r *http.Request, c *ownerContext, f url.Values) error {
	store, ok := s.Host.Store().(state.ProductOnboardingStore)
	if !ok {
		return state.ErrNotFound
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return err
	}
	cookie, err := r.Cookie(onboardingOwnerCookie(c, realm.authApp.ID))
	if err != nil {
		return customerAuthFailure{http.StatusUnauthorized}
	}
	req := api.ProductOnboardingRequest{ID: f.Get("id"), TenantID: f.Get("tenant"), ExternalRef: f.Get("external_ref"), Name: f.Get("name"), Plan: f.Get("plan"), OrganizationID: f.Get("organization")}
	req, err = s.verifyOnboardingWorkspace(r.Context(), realm, cookie.Value, req)
	if err != nil {
		return err
	}
	p, err := store.PreviewProductOnboarding(r.Context(), c.account.ID, c.app.ID, req)
	if err != nil {
		return err
	}
	confirmation := api.ProductOnboardingConfirmation{Request: p.Request, ExpectedHash: p.Hash}
	payload := onboardingOwnerPayload(confirmation)
	token, err := s.ownerToken(w, c, "onboarding_confirm", ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	before := "Existing customer"
	if p.CreatesTenant {
		before = "New customer"
	}
	view := &dashboard.ProductControlConfirmation{Title: "Review customer setup", Description: "Confirm this customer's workspace and product terms. Workspace members receive access according to the product's current role permissions. Existing assignments use the separate plan-change flow.", Payload: payload, Token: token, ActionURL: c.data.BaseURL + "/onboarding/confirm", Details: []dashboard.ProductControlComparison{{Feature: "Customer", Before: before, After: p.Tenant.Name}, {Feature: "Workspace", After: p.Request.OrganizationName}, {Feature: "Plan", After: p.Request.Plan}, {Feature: "Currency", After: p.Catalog.Spec.Currency}}}
	names := []string{}
	for name := range p.Catalog.Spec.Features {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		view.Details = append(view.Details, dashboard.ProductControlComparison{Feature: p.Catalog.Spec.Features[name].Label, After: ownerGrantDescription(p.Catalog, api.ProductCustomer{Plan: p.Request.Plan}, name)})
	}
	c.data.Confirmation = view
	return nil
}

func onboardingOwnerPayload(c api.ProductOnboardingConfirmation) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOnboardingOwnerPayload(payload string) (api.ProductOnboardingConfirmation, error) {
	var c api.ProductOnboardingConfirmation
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return c, state.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || onboardingOwnerPayload(c) != payload {
		return c, state.ErrInvalidArgument
	}
	return c, nil
}

func (s *Service) OwnerOnboardingConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormLimit(w, r, []string{"csrf_token", "payload"}, api.ProductControlOnboardingFileMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "onboarding_confirm", ownerConfirmationSubject(c, f.Get("payload")))
	}
	var confirmation api.ProductOnboardingConfirmation
	if err == nil {
		confirmation, err = decodeOnboardingOwnerPayload(f.Get("payload"))
	}
	var receipt api.ProductOnboardingReceipt
	if err == nil {
		receipt, err = s.confirmOwnerOnboarding(r, c, confirmation)
	}
	if err != nil {
		s.ownerOnboardingError(w, r, c, err)
		return
	}
	http.Redirect(w, r, c.data.BaseURL+"/onboarding?id="+url.QueryEscape(receipt.ID), http.StatusSeeOther)
}

func (s *Service) confirmOwnerOnboarding(r *http.Request, c *ownerContext, confirmation api.ProductOnboardingConfirmation) (api.ProductOnboardingReceipt, error) {
	store, ok := s.Host.Store().(state.ProductOnboardingStore)
	if !ok {
		return api.ProductOnboardingReceipt{}, state.ErrNotFound
	}
	token := ""
	if cookie, e := r.Cookie(onboardingOwnerCookie(c, confirmation.Request.AuthAppID)); e == nil {
		token = cookie.Value
	}
	return s.applyOnboarding(r, c.account, c.app, store, confirmation, token)
}

func (s *Service) ownerOnboardingError(w http.ResponseWriter, r *http.Request, c *ownerContext, err error) {
	var auth customerAuthFailure
	var quota *state.PlatformTenantQuotaError
	if errors.As(err, &auth) || errors.As(err, &quota) || errors.Is(err, state.ErrProductOnboardingStale) || errors.Is(err, state.ErrProductAccess) {
		status, message := http.StatusConflict, "Customer setup changed. Open onboarding and review again."
		if errors.As(err, &auth) {
			status = auth.status
			message = "Sign in to Application Auth with a workspace owner or admin account. If sign-in is unavailable, check the Auth app and try again."
		}
		if errors.As(err, &quota) {
			status = http.StatusForbidden
			message = "This account has reached its customer limit."
		}
		if errors.Is(err, state.ErrProductAccess) {
			status = http.StatusForbidden
			message = "Customer setup requires an active account and customer with an eligible platform plan."
		}
		c.data.Error = message
		c.data.Confirmation = nil
		c.data.Onboarding = nil
		c.data.Main = false
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		s.OwnerRender(w, r, c.account, c.data)
		return
	}
	s.ownerFormError(w, r, c, err)
}
