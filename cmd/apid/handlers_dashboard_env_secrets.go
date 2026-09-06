package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardEnvMutationAction    = "dashboard_env_mutation"
	dashboardSecretMutationAction = "dashboard_secret_mutation"
	dashboardEnvCSRFCookie        = "faas_csrf_dashboard_env"
	dashboardSecretCSRFCookie     = "faas_csrf_dashboard_secret"
)

// parseAppEnvSecretsPath recognizes both canonical links for the combined
// editor. Keeping /env and /secrets as aliases lets customers deep-link from
// either API surface while one page keeps the two stores in sync.
func parseAppEnvSecretsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	for _, suffix := range []string{"/env", "/secrets"} {
		if !strings.HasSuffix(rest, suffix) {
			continue
		}
		slug := strings.TrimSuffix(rest, suffix)
		if slug != "" && !strings.Contains(slug, "/") {
			return slug, true
		}
	}
	return "", false
}

// renderAppEnvSecrets renders the combined env + secrets editor. Env values
// are non-sensitive runtime configuration and remain editable in place;
// secret values are deliberately absent from the projection and only enter
// the transient write/rotate request body.
func (s *server) renderAppEnvSecrets(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	selectedScope, prob := dashboardScope(r.URL.Query().Get("scope"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	envRows, secretRows := s.dashboardConfigRows(ctx, log, acct, app, selectedScope)
	envCount, err := s.store.CountAppEnv(ctx, acct.ID, app.ID)
	if err != nil {
		log.Warn("dashboard renderAppEnvSecrets: count env", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	secretCount, err := s.store.CountAppSecrets(ctx, acct.ID, app.ID)
	if err != nil {
		log.Warn("dashboard renderAppEnvSecrets: count secrets", "account_id", acct.ID, "app_id", app.ID, "err", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppEnvSecrets: count deployed apps", "account_id", acct.ID, "err", err)
	}
	envTok := s.issueDashboardConfigCSRF(w, log, dashboardEnvMutationAction, dashboardEnvCSRFCookie, acct.ID, app.ID)
	secretTok := s.issueDashboardConfigCSRF(w, log, dashboardSecretMutationAction, dashboardSecretCSRFCookie, acct.ID, app.ID)
	writeScope := selectedScope
	if writeScope == api.EnvScopeAllSentinel {
		writeScope = "default"
	}
	data := dashboard.EnvSecretsData{
		AppSlug: app.Slug, AppStatus: string(app.Status), SelectedScope: selectedScope,
		WriteScope: writeScope, ScopeOptions: dashboardConfigScopes(envRows, secretRows, selectedScope),
		Env: projectDashboardEnv(envRows, selectedScope), Secrets: projectDashboardSecrets(secretRows, selectedScope),
		EnvCount: envCount, EnvQuota: limits.EnvVarsMax, SecretCount: secretCount,
		SecretQuota: limits.SecretCountMax, EnvCSRF: envTok, SecretCSRF: secretTok,
		Flash: dashboardConfigFlash(r.URL.Query().Get("changed")),
	}
	page := dashboard.Page{
		Title:   app.Slug + " environment",
		Body:    "env_secrets",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardScope(raw string) (string, *api.Problem) {
	if raw == "" {
		return "default", nil
	}
	if raw == api.EnvScopeAllSentinel {
		return raw, nil
	}
	if prob := api.ValidateScope(raw); prob != nil {
		return "", prob
	}
	return raw, nil
}

func (s *server) dashboardConfigRows(ctx stdctx, log *slog.Logger, acct state.Account, app state.App, scope string) ([]state.AppEnv, []state.AppSecret) {
	var envRows []state.AppEnv
	var secretRows []state.AppSecret
	var err error
	if scope == api.EnvScopeAllSentinel {
		envRows, err = s.store.ListAllAppEnv(ctx, acct.ID, app.ID)
		if err != nil {
			log.Warn("dashboard renderAppEnvSecrets: list all env", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
		secretRows, err = s.store.ListAllAppSecrets(ctx, acct.ID, app.ID)
		if err != nil {
			log.Warn("dashboard renderAppEnvSecrets: list all secrets", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
		return envRows, secretRows
	}
	envRows, err = s.store.ListAppEnvInScope(ctx, acct.ID, app.ID, scope)
	if err != nil {
		log.Warn("dashboard renderAppEnvSecrets: list env", "account_id", acct.ID, "app_id", app.ID, "scope", scope, "err", err)
	}
	secretRows, err = s.store.ListAppSecretsInScope(ctx, acct.ID, app.ID, scope)
	if err != nil {
		log.Warn("dashboard renderAppEnvSecrets: list secrets", "account_id", acct.ID, "app_id", app.ID, "scope", scope, "err", err)
	}
	return envRows, secretRows
}

func projectDashboardEnv(rows []state.AppEnv, selectedScope string) []dashboard.EnvItem {
	items := make([]dashboard.EnvItem, 0, len(rows))
	for _, row := range rows {
		scope := row.Scope
		if scope == "" {
			scope = selectedScope
		}
		items = append(items, dashboard.EnvItem{Scope: scope, Key: row.Key, Value: row.Value,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Scope+"\x00"+items[i].Key < items[j].Scope+"\x00"+items[j].Key })
	return items
}

func projectDashboardSecrets(rows []state.AppSecret, selectedScope string) []dashboard.SecretItem {
	items := make([]dashboard.SecretItem, 0, len(rows))
	for _, row := range rows {
		scope := row.Scope
		if scope == "" {
			scope = selectedScope
		}
		prefix := row.ValueHash
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		items = append(items, dashboard.SecretItem{Scope: scope, Key: row.Key, ValueHashPrefix: prefix,
			Kid: row.Kid, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
			Managed: row.ManagedPostgresBindingID != ""})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Scope+"\x00"+items[i].Key < items[j].Scope+"\x00"+items[j].Key })
	return items
}

func dashboardConfigScopes(env []state.AppEnv, secrets []state.AppSecret, selected string) []string {
	seen := map[string]bool{"default": true}
	for _, row := range env {
		if row.Scope != "" && row.Scope != api.EnvScopeAllSentinel {
			seen[row.Scope] = true
		}
	}
	for _, row := range secrets {
		if row.Scope != "" && row.Scope != api.EnvScopeAllSentinel {
			seen[row.Scope] = true
		}
	}
	if selected != "" && selected != api.EnvScopeAllSentinel {
		seen[selected] = true
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func dashboardConfigFlash(changed string) string {
	switch changed {
	case "env-set":
		return "Environment variable saved. A future wake will use the new value."
	case "env-delete":
		return "Environment variable deleted."
	case "secret-set":
		return "Secret saved. Its value is write-only and will not be shown here."
	case "secret-delete":
		return "Secret deleted."
	case "secret-rotate":
		return "Secret rotated. Store the new value safely; it cannot be read back."
	case "error":
		return "The change could not be applied. See the error response for details."
	default:
		return ""
	}
}

func (s *server) issueDashboardConfigCSRF(w http.ResponseWriter, log *slog.Logger, action, cookieName, accountID, appID string) string {
	if s.sessions == nil {
		return ""
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, action, accountID, cookieName)
	if err != nil {
		log.Warn("dashboard env/secrets: issue csrf", "action", action, "account_id", accountID, "app_id", appID, "err", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token
}

func (s *server) dashboardSetEnv(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardEnvMutationAction, dashboardEnvCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	scope := formScope(r)
	key := r.PostFormValue("key")
	value := r.PostFormValue("value")
	resp := s.forwardDashboardJSON(r, acct, http.MethodPut, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/env/"+url.PathEscape(key), scope, key, api.PutAppEnvRequest{Value: value}, s.setEnv)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "env-set", scope)
}

func (s *server) dashboardDeleteEnv(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardEnvMutationAction, dashboardEnvCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	scope := formScope(r)
	key := r.PathValue("key")
	resp := s.forwardDashboardJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/env/"+url.PathEscape(key), scope, key, nil, s.deleteEnv)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "env-delete", scope)
}

func (s *server) dashboardSetSecret(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardSecretMutationAction, dashboardSecretCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	scope := formScope(r)
	key := r.PostFormValue("key")
	value := r.PostFormValue("value")
	resp := s.forwardDashboardJSON(r, acct, http.MethodPut, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/secrets/"+url.PathEscape(key), scope, key, api.PutAppSecretRequest{Value: value}, s.setSecret)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "secret-set", scope)
}

func (s *server) dashboardDeleteSecret(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardSecretMutationAction, dashboardSecretCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	scope := formScope(r)
	key := r.PathValue("key")
	resp := s.forwardDashboardJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/secrets/"+url.PathEscape(key), scope, key, nil, s.deleteSecret)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "secret-delete", scope)
}

func (s *server) dashboardRotateSecret(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardSecretMutationAction, dashboardSecretCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	scope := formScope(r)
	key := r.PathValue("key")
	value := r.PostFormValue("value")
	resp := s.forwardDashboardJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/secrets/"+url.PathEscape(key)+"/rotate", scope, key, api.RotateAppSecretRequest{Value: value}, s.rotateAppSecret)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "secret-rotate", scope)
}

func formScope(r *http.Request) string {
	if scope := r.PostFormValue("scope"); scope != "" {
		return scope
	}
	return "default"
}

func (s *server) verifyDashboardConfigCSRF(w http.ResponseWriter, r *http.Request, action, cookieName, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, action, accountID, cookieName); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

type dashboardJSONHandler func(http.ResponseWriter, *http.Request, state.Account)

func (s *server) forwardDashboardJSON(r *http.Request, acct state.Account, method, path, scope, key string, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method = method
	req.URL = cloneDashboardURL(r.URL, path, scope)
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", r.PathValue("slug"))
	req.SetPathValue("key", key)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}

func cloneDashboardURL(src *url.URL, path, scope string) *url.URL {
	u := *src
	u.Path = path
	u.RawPath = ""
	values := url.Values{}
	if scope != "" {
		values.Set("scope", scope)
	}
	u.RawQuery = values.Encode()
	return &u
}

func dashboardMutationSucceeded(w http.ResponseWriter, resp *httptest.ResponseRecorder) bool {
	if resp.Code >= http.StatusOK && resp.Code < http.StatusMultipleChoices {
		return true
	}
	for key, values := range resp.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.Code)
	_, _ = w.Write(resp.Body.Bytes())
	return false
}

func redirectDashboardConfig(w http.ResponseWriter, r *http.Request, slug, changed, scope string) {
	values := url.Values{"changed": []string{changed}}
	if scope != "" {
		values.Set("scope", scope)
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/env?"+values.Encode(), http.StatusFound)
}
