package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) populateDashboardSecretReferences(ctx context.Context, log *slog.Logger, acct state.Account, app state.App, data *dashboard.EnvSecretsData) {
	if app.ProjectID == "" {
		return
	}
	store, supported := s.store.(state.AppEnvironmentSecretReferenceControlStore)
	environments, err := s.store.ListProjectEnvironments(ctx, acct.ID, app.ProjectID)
	if err != nil || !supported {
		data.SecretReferenceError = "Could not load environment secret references. Reload this page before editing them."
		return
	}
	for _, environment := range environments {
		if api.ValidateScope(environment.Slug) != nil {
			data.SecretReferenceUnsupportedEnvironments = append(data.SecretReferenceUnsupportedEnvironments, environment.Slug)
			continue
		}
		data.SecretReferenceEnvironments = append(data.SecretReferenceEnvironments, environment.Slug)
		if !slices.Contains(data.ScopeOptions, environment.Slug) {
			data.ScopeOptions = append(data.ScopeOptions, environment.Slug)
		}
		if data.SelectedScope != api.EnvScopeAllSentinel && data.SelectedScope != environment.Slug {
			continue
		}
		snapshot, err := store.ReadAppEnvironmentSecretReferences(ctx, acct.ID, app.ID, environment.Slug)
		if err != nil {
			log.Warn("dashboard secret references unavailable", "app_id", app.ID, "environment", environment.Slug)
			data.SecretReferenceError = "Could not load environment secret references. Reload this page before editing them."
			data.SecretReferences = nil
			return
		}
		for key, reference := range snapshot.References {
			data.SecretReferences = append(data.SecretReferences, dashboard.SecretReferenceItem{Environment: environment.Slug, Key: key, Reference: reference})
		}
		for _, key := range snapshot.SuppressedKeys {
			data.SecretReferences = append(data.SecretReferences, dashboard.SecretReferenceItem{Environment: environment.Slug, Key: key, Suppressed: true})
		}
	}
	sort.Strings(data.ScopeOptions)
	sort.Strings(data.SecretReferenceEnvironments)
	sort.Strings(data.SecretReferenceUnsupportedEnvironments)
	sort.Slice(data.SecretReferences, func(i, j int) bool {
		left, right := data.SecretReferences[i], data.SecretReferences[j]
		return left.Environment+"/"+left.Key < right.Environment+"/"+right.Key
	})
}

func (s *server) dashboardSetSecretReference(w http.ResponseWriter, r *http.Request) {
	s.dashboardSecretReferenceMutation(w, r, false)
}

func (s *server) dashboardDeleteSecretReference(w http.ResponseWriter, r *http.Request) {
	s.dashboardSecretReferenceMutation(w, r, true)
}

func (s *server) dashboardSecretReferenceMutation(w http.ResponseWriter, r *http.Request, remove bool) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardSecretMutationAction, dashboardSecretCSRFCookie, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return
	}
	environment, key := r.PostFormValue("environment"), r.PostFormValue("key")
	method, handler := http.MethodPut, s.setAppSecretReference
	var body any = api.PutAppSecretReferenceRequest{Reference: r.PostFormValue("reference")}
	if remove {
		method, handler, key, body = http.MethodDelete, s.deleteAppSecretReference, r.PathValue("key"), nil
	}
	guarded := s.auth(s.requireScope(api.ScopesSecretsWriteSurface...)(func(w http.ResponseWriter, request *http.Request, current state.Account) {
		if current.ID != acct.ID {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account changed", "Reload the dashboard before editing this environment."))
			return
		}
		handler(w, request, current)
	}))
	forward := func(w http.ResponseWriter, request *http.Request, account state.Account) {
		query := request.URL.Query()
		query.Set("environment", environment)
		request.URL.RawQuery = query.Encode()
		// The dashboard's verified cookie remains the identity for this form.
		// Reauthentication supplies the JSON route's scope/MFA context.
		request.Header.Del("Authorization")
		guarded(w, request)
	}
	response := s.forwardDashboardJSON(r, acct, method, "/v1/apps/"+url.PathEscape(r.PathValue("slug"))+"/secret-references/"+url.PathEscape(key), "", key, body, forward)
	if !dashboardMutationSucceeded(w, response) {
		return
	}
	redirectDashboardConfig(w, r, r.PathValue("slug"), "reference-changed", environment)
}
