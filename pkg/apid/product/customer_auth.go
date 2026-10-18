package product

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

type customerRealm struct {
	app, authApp state.App
	account      state.Account
	origin       string
	store        state.ProductControlStore
	catalog      api.ProductCatalog
}

type customerAuthFailure struct{ status int }

func (e customerAuthFailure) Error() string { return "customer authentication unavailable" }

// The realm always comes from a live, account-owned Application Auth app.
// Request bodies and JWT claims never select an upstream host or account.
func (s *Service) customerRealm(r *http.Request) (customerRealm, error) {
	app, err := s.Host.Store().AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return customerRealm{}, err
	}
	account, err := s.Host.Store().AccountByID(r.Context(), app.AccountID)
	if err != nil {
		return customerRealm{}, err
	}
	if !account.Active() {
		return customerRealm{}, state.ErrProductAccess
	}
	store, ok := s.Host.Store().(state.ProductControlStore)
	if !ok || s.AuthOrigin == nil {
		return customerRealm{}, customerAuthFailure{http.StatusServiceUnavailable}
	}
	catalog, err := store.GetProductCatalog(r.Context(), account.ID, app.ID, "")
	if err != nil {
		return customerRealm{}, err
	}
	if catalog.Spec.AuthApp == "" {
		return customerRealm{}, state.ErrNotFound
	}
	authApp, origin, err := s.AuthOrigin(r.Context(), account, catalog.Spec.AuthApp)
	if err != nil {
		return customerRealm{}, err
	}
	return customerRealm{app: app, authApp: authApp, account: account, origin: origin, store: store, catalog: catalog}, nil
}

func customerSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Scheme != "https" || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Origin rejected", "use the customer portal on this HTTPS origin"))
		return false
	}
	return true
}

func (s *Service) CustomerLogin(w http.ResponseWriter, r *http.Request) {
	if !customerSameOrigin(w, r) {
		return
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !operation.DecodeOperationBody(w, r, &body, api.ProductControlAuthBodyBytes) {
		return
	}
	if body.Email == "" || body.Password == "" {
		customerError(w, customerAuthFailure{http.StatusUnauthorized})
		return
	}
	s.customerToken(w, r, realm, "/auth/v1/token?grant_type=password", body)
}

func (s *Service) CustomerRefresh(w http.ResponseWriter, r *http.Request) {
	if !customerSameOrigin(w, r) {
		return
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	refresh, err := r.Cookie(realm.cookieName("refresh"))
	if err != nil {
		customerError(w, customerAuthFailure{http.StatusUnauthorized})
		return
	}
	s.customerToken(w, r, realm, "/auth/v1/token?grant_type=refresh_token", map[string]string{"refresh_token": refresh.Value})
}

func (s *Service) customerToken(w http.ResponseWriter, r *http.Request, realm customerRealm, path string, body any) {
	var session struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := s.authCall(r.Context(), realm, http.MethodPost, path, "", body, &session); err != nil {
		customerError(w, err)
		return
	}
	if !validCookieToken(session.AccessToken) || !validCookieToken(session.RefreshToken) || session.ExpiresIn <= 0 || session.ExpiresIn > api.ProductControlSessionMaxSeconds {
		customerError(w, customerAuthFailure{http.StatusServiceUnavailable})
		return
	}
	// Verify the session against the Auth database before accepting it. This
	// catches revoked/banned users even if a JWT's expiry has not passed.
	if _, err := s.customerOrganizations(r.Context(), realm, session.AccessToken); err != nil {
		customerError(w, err)
		return
	}
	realm.setCookie(w, "access", session.AccessToken, session.ExpiresIn)
	realm.setCookie(w, "refresh", session.RefreshToken, api.ProductControlSessionMaxSeconds)
	httpkit.WriteJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Service) CustomerLogout(w http.ResponseWriter, r *http.Request) {
	if !customerSameOrigin(w, r) {
		return
	}
	// Local sign-out must also work when the Auth app, account or binding is
	// unavailable. Clear received product cookies before any fallible reads.
	clearCustomerCookies(w, r)
	realm, err := s.customerRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	access, accessErr := r.Cookie(realm.cookieName("access"))
	token := ""
	if accessErr == nil {
		token = access.Value
	}
	if token == "" {
		if refresh, cookieErr := r.Cookie(realm.cookieName("refresh")); cookieErr == nil {
			var session struct {
				AccessToken string `json:"access_token"`
			}
			err = s.authCall(r.Context(), realm, http.MethodPost, "/auth/v1/token?grant_type=refresh_token", "", map[string]string{"refresh_token": refresh.Value}, &session)
			token = session.AccessToken
		}
	}
	if token != "" {
		err = s.authCall(r.Context(), realm, http.MethodPost, "/auth/v1/logout?scope=local", token, nil, nil)
	}
	if err != nil {
		customerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func clearCustomerCookies(w http.ResponseWriter, r *http.Request) {
	for _, cookie := range r.Cookies() {
		if strings.HasPrefix(cookie.Name, "__Secure-gregale-pc-") {
			http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: "/v1/product-control/customer/" + url.PathEscape(r.PathValue("slug")) + "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		}
	}
}

func (realm customerRealm) cookieName(kind string) string {
	digest := sha256.Sum256([]byte(realm.authApp.ID))
	return fmt.Sprintf("__Secure-gregale-pc-%x-%s", digest[:8], kind)
}
func (realm customerRealm) setCookie(w http.ResponseWriter, kind, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: realm.cookieName(kind), Value: value,
		Path: "/v1/product-control/customer/" + realm.app.Slug + "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func validCookieToken(token string) bool {
	if token == "" || len(token) > api.ProductControlCookieTokenMaxBytes {
		return false
	}
	for _, c := range token {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("._-", c) {
			return false
		}
	}
	return true
}

func (s *Service) authCall(ctx context.Context, realm customerRealm, method, path, token string, body, out any) error {
	authorization := ""
	if token != "" {
		authorization = "Bearer " + token
	}
	return s.authCallAuthorization(ctx, realm, method, path, authorization, body, out)
}

func (s *Service) authCallAuthorization(ctx context.Context, realm customerRealm, method, path, authorization string, body, out any) error {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.ProductControlAuthTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, realm.origin+path, bytes.NewReader(encoded))
	if err != nil {
		return customerAuthFailure{http.StatusServiceUnavailable}
	}
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	client := http.Client{Timeout: time.Duration(api.ProductControlAuthTimeoutSeconds) * time.Second}
	if s.AuthClient != nil {
		client = *s.AuthClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return customerAuthFailure{http.StatusServiceUnavailable}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == 400 || response.StatusCode == 401 {
			return customerAuthFailure{http.StatusUnauthorized}
		}
		if response.StatusCode == 403 {
			return customerAuthFailure{http.StatusForbidden}
		}
		return customerAuthFailure{http.StatusServiceUnavailable}
	}
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, api.ProductControlAuthResponseBytes+1))
	if err != nil || len(data) > api.ProductControlAuthResponseBytes || json.Unmarshal(data, out) != nil {
		return customerAuthFailure{http.StatusServiceUnavailable}
	}
	return nil
}

type customerOrganization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func (s *Service) customerOrganizations(ctx context.Context, realm customerRealm, token string) ([]customerOrganization, error) {
	var reply struct {
		Organizations []customerOrganization `json:"organizations"`
	}
	if err := s.authCall(ctx, realm, http.MethodGet, "/authz/v1/orgs", token, nil, &reply); err != nil {
		return nil, err
	}
	if reply.Organizations == nil || len(reply.Organizations) > api.ProductControlAuthMaxOrganizations {
		return nil, customerAuthFailure{http.StatusServiceUnavailable}
	}
	seen := map[string]bool{}
	for _, org := range reply.Organizations {
		if id, err := uuid.Parse(org.ID); err != nil || id == uuid.Nil || id.String() != org.ID || seen[org.ID] || !productcontrol.ValidAccessRole(org.Role) {
			return nil, customerAuthFailure{http.StatusServiceUnavailable}
		}
		seen[org.ID] = true
	}
	return reply.Organizations, nil
}

type customerWorkspace struct {
	OrganizationID string                    `json:"organization_id"`
	Name           string                    `json:"name"`
	TenantID       string                    `json:"tenant_id"`
	Access         api.ProductCustomerAccess `json:"access"`
}

func (s *Service) customerWorkspaces(r *http.Request, realm customerRealm) ([]customerWorkspace, error) {
	cookie, err := r.Cookie(realm.cookieName("access"))
	if err != nil {
		return nil, customerAuthFailure{http.StatusUnauthorized}
	}
	orgs, err := s.customerOrganizations(r.Context(), realm, cookie.Value)
	if err != nil {
		return nil, err
	}
	workspaces := []customerWorkspace{}
	for _, org := range orgs {
		permissions := productcontrol.EffectiveAccess(realm.catalog.Spec.Access, org.Role)
		if permissions == (api.ProductPermissions{}) {
			continue
		}
		customer, err := realm.store.ProductCustomerByAuthOrganization(r.Context(), realm.account.ID, realm.app.ID, org.ID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		tenants, ok := s.Host.Store().(state.PlatformTenantStore)
		if !ok {
			return nil, customerAuthFailure{http.StatusServiceUnavailable}
		}
		tenant, err := tenants.GetPlatformTenant(r.Context(), realm.account.ID, customer.TenantID)
		if errors.Is(err, state.ErrNotFound) || err == nil && tenant.Status != state.PlatformTenantActive {
			continue
		}
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, customerWorkspace{org.ID, org.Name, customer.TenantID, api.ProductCustomerAccess{Role: org.Role, PolicyRevision: realm.catalog.Revision, Permissions: permissions}})
	}
	return workspaces, nil
}

func (s *Service) CustomerWorkspaces(w http.ResponseWriter, r *http.Request) {
	realm, err := s.customerRealm(r)
	if err != nil {
		customerError(w, err)
		return
	}
	workspaces, err := s.customerWorkspaces(r, realm)
	if err != nil {
		customerError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"workspaces": workspaces, "receipt_max_bytes": api.ProductControlReceiptMaxBytes, "receipt_max_entries": api.ProductControlRecoveryMaxEntries, "replay_seconds": api.ProductControlReplaySeconds, "help_text_max_bytes": api.ProductControlHelpTextMaxBytes, "help_page_size": api.ProductControlHelpPageSize, "notification_page_size": api.ProductControlNotificationPageSize, "work_history_page_size": api.ProductControlWorkHistoryPageSize, "work_history_cursor_max_bytes": api.ProductControlWorkHistoryCursorMaxBytes, "activity_page_size": api.ProductControlWorkspaceActivityPageSize, "activity_cursor_max_bytes": api.ProductControlOwnerCursorMaxBytes})
}

// Customer is the separate Application Auth boundary. It installs only a
// tenant principal after live session + organization membership verification;
// an Application Auth JWT is never accepted as a platform-owner credential.
func (s *Service) Customer(permission api.ProductPermission, next httpkit.AccountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !customerSameOrigin(w, r) {
			return
		}
		realm, err := s.customerRealm(r)
		if err != nil {
			customerError(w, err)
			return
		}
		workspaces, err := s.customerWorkspaces(r, realm)
		if err != nil {
			customerError(w, err)
			return
		}
		var selected customerWorkspace
		for _, workspace := range workspaces {
			if workspace.OrganizationID == r.Header.Get("X-Product-Organization") {
				selected = workspace
				break
			}
		}
		if selected.TenantID == "" {
			customerError(w, state.ErrProductAccess)
			return
		}
		if !customerPermissionAllowed(w, selected.Access.Permissions, permission) || !s.customerOperationAllowed(w, r, realm, selected.TenantID) {
			return
		}
		*r = *r.WithContext(authmw.WithProductCustomer(r.Context(), realm.account, authmw.ProductCustomerIdentity{TenantID: selected.TenantID, OrganizationID: selected.OrganizationID, AuthApp: realm.authApp.Slug, AuthAppID: realm.authApp.ID, ProductSlug: realm.app.Slug, Access: selected.Access}))
		next(w, r, realm.account)
	}
}

func customerPermissionAllowed(w http.ResponseWriter, permissions api.ProductPermissions, permission api.ProductPermission) bool {
	allowed := permission == "" && permissions != (api.ProductPermissions{}) ||
		permission == api.ProductPermissionViewWork && permissions.ViewWork ||
		permission == api.ProductPermissionRunWork && permissions.RunWork ||
		permission == api.ProductPermissionViewBilling && permissions.ViewBilling ||
		permission == api.ProductPermissionManagePlan && permissions.ManagePlan ||
		permission == api.ProductPermissionManageTeam && permissions.ManageTeam
	if !allowed {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "product_permission_denied", "Product permission required", "your workspace role does not allow this action; contact your product team"))
	}
	return allowed
}

func (s *Service) customerOperationAllowed(w http.ResponseWriter, r *http.Request, realm customerRealm, tenant string) bool {
	if r.PathValue("id") == "" {
		return true
	}
	store, ok := s.Host.Store().(state.OperationStore)
	if !ok {
		customerError(w, customerAuthFailure{http.StatusServiceUnavailable})
		return false
	}
	op, err := store.OperationByID(r.Context(), realm.account.ID, tenant, r.PathValue("id"))
	// Expired rows no longer expose their app through OperationByID. Collapse
	// that response too, so another app cannot disclose an expired run's ID.
	if errors.Is(err, state.ErrOperationExpired) || err == nil && op.AppID != realm.app.ID {
		err = state.ErrNotFound
	}
	if err != nil {
		customerError(w, err)
		return false
	}
	return true
}

func (s *Service) CustomerPortal(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if identity, ok := authmw.ProductCustomerFromContext(r.Context()); ok {
		if store, available := s.Host.Store().(state.ProductOnboardingStore); available {
			app, err := s.Host.Store().AppBySlug(r.Context(), r.PathValue("slug"))
			if err == nil {
				err = store.ObserveProductCustomerAccess(r.Context(), acct.ID, app.ID, identity.TenantID, identity.AuthApp, identity.OrganizationID)
			}
			if err != nil {
				customerError(w, err)
				return
			}
		}
	}
	query := r.URL.Query()
	query.Set("app", r.PathValue("slug"))
	r.URL.RawQuery = query.Encode()
	s.Portal(w, r, acct)
}
func (s *Service) CustomerStart(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductStartRequest
	if !operation.DecodeOperationBody(w, r, &req, api.OperationSubmissionMaxBytes+api.OperationStartBodyOverheadBytes) {
		return
	}
	if req.App != "" && req.App != r.PathValue("slug") {
		customerError(w, state.ErrNotFound)
		return
	}
	req.App = r.PathValue("slug")
	req, ok := s.resolveStart(w, r, acct, req)
	if ok {
		s.StartOperation(w, r, acct, req)
	}
}

func customerError(w http.ResponseWriter, err error) {
	var auth customerAuthFailure
	if errors.As(err, &auth) {
		api.WriteProblem(w, api.NewProblem(auth.status, "product_customer_auth", "Customer session unavailable", "sign in again or retry when your product's login service is available"))
		return
	}
	respond(w, nil, err)
}
