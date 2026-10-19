// Package mcpreadiness records server-executed public deployment probes.
package mcpreadiness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/mcpcatalog"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type Handler struct {
	PagerDutyAcknowledgementSeal func(context.Context, string) ([]byte, *api.Problem)
	PagerDutySeal                func(context.Context, string) ([]byte, *api.Problem)
	FallbackURL                  func(context.Context, string) *api.Problem
	FallbackSeal                 func(context.Context, string) ([]byte, *api.Problem)
	Audit                        MaintenanceAudit
	Identities                   func(context.Context) []*age.X25519Identity
	Loader                       httpkit.AppLoader
	Store                        state.Store
	Endpoint                     func(context.Context, state.App, state.Deployment) (string, error)
	Client                       *http.Client
}

func New(store state.Store, endpoint func(context.Context, state.App, state.Deployment) (string, error), loader httpkit.AppLoader) *Handler {
	// Probes dial customer deployments through the egress guard, never through
	// a replaced process-wide transport. A replaced http.DefaultTransport must
	// not panic apid's handler construction.
	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.Proxy = nil
	transport.DialContext = oci.EgressDialContext(nil)
	return &Handler{Store: store, Loader: loader, Endpoint: endpoint, Client: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "MCP readiness not found", "No matching owned application or deployment"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("Invalid MCP readiness request"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "MCP readiness changed", "Reload the policy and deployment before retrying"))
	default:
		api.WriteProblem(w, api.ErrCapacity("MCP readiness unavailable"))
	}
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		problem(w, state.ErrInvalidArgument)
		return false
	}
	return true
}
func (h *Handler) Policy(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := h.Store.(state.MCPReadinessStore)
	if !ok {
		problem(w, errors.New("store unavailable"))
		return
	}
	app, err := h.Store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, allowed := h.Loader.LoadApp(w, r, account, app.Slug); !allowed {
			return
		}
	}
	scope := api.MCPConnectionScope(r.URL.Query().Get("scope"))
	if len(r.URL.Query()["scope"]) > 1 || api.ValidateScope(scope) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if r.Method == http.MethodGet {
		p, e := store.GetMCPReadinessPolicy(r.Context(), account.ID, app.ID, scope)
		if e != nil {
			problem(w, e)
			return
		}
		httpkit.WriteJSON(w, 200, p)
		return
	}
	var request api.SetMCPReadinessPolicyRequest
	if !decode(w, r, &request) {
		return
	}
	if r.URL.Query().Has("scope") {
		if request.Scope != "" && request.Scope != scope {
			problem(w, state.ErrInvalidArgument)
			return
		}
		request.Scope = scope
	}
	if e := api.ValidateMCPReadinessPolicy(request); e != nil {
		api.WriteProblem(w, api.ErrValidation(e.Error()))
		return
	}
	scheduled := false
	for _, role := range request.Roles {
		if role.Monitor != nil && role.Monitor.Enabled {
			scheduled = true
		}
	}
	if scheduled && app.PublicAuthMode == state.AppPublicAuthModeInternalOnly {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if (request.Enabled || scheduled) && !app.AcceptsRequestInvocations() {
		problem(w, state.ErrInvalidArgument)
		return
	}
	p, err := store.SetMCPReadinessPolicy(r.Context(), account.ID, app.ID, request)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, p)
}
func (h *Handler) Deployment(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tokenValues := r.Header.Values(api.MCPReadinessTokenHeader)
	r.Header.Del(api.MCPReadinessTokenHeader)
	store, ok := h.Store.(state.MCPReadinessStore)
	if !ok {
		problem(w, errors.New("store unavailable"))
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	deployment, err := h.Store.DeploymentByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) {
		deployment, err = h.Store.DeploymentByID(r.Context(), strings.ReplaceAll(id.String(), "-", ""))
	}
	if err != nil {
		problem(w, state.ErrNotFound)
		return
	}
	app, err := h.Store.AppByID(r.Context(), deployment.AppID)
	if err != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, allowed := h.Loader.LoadApp(w, r, account, app.Slug); !allowed {
			return
		}
	}
	if r.Method == http.MethodGet {
		snapshot, e := store.ListMCPReadiness(r.Context(), account.ID, app.ID, deployment.ID)
		if e != nil {
			problem(w, e)
			return
		}
		if monitors, ok := h.Store.(state.MCPReadinessMonitorStore); ok {
			snapshot.Monitoring, e = monitors.ListMCPReadinessMonitoring(r.Context(), account.ID, app.ID, deployment.ID)
			if e != nil {
				problem(w, e)
				return
			}
		}
		httpkit.WriteJSON(w, 200, snapshot)
		return
	}
	var request api.MCPReadinessProbeRequest
	if !decode(w, r, &request) {
		return
	}
	if len(tokenValues) > 1 || (len(tokenValues) == 1 && !api.ValidMCPProbeToken(tokenValues[0])) {
		problem(w, state.ErrInvalidArgument)
		return
	}
	work, stop := context.WithTimeout(r.Context(), 30*time.Second)
	defer stop()
	subject, err := store.MCPReadinessSubject(work, account.ID, deployment.ID)
	if err != nil {
		problem(w, err)
		return
	}
	var role *api.MCPReadinessRole
	for i := range subject.Policy.Roles {
		if subject.Policy.Roles[i].Name == request.Role {
			role = &subject.Policy.Roles[i]
			break
		}
	}
	if role == nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if h.Endpoint == nil {
		problem(w, errors.New("preview unavailable"))
		return
	}
	endpoint, err := h.Endpoint(work, app, deployment)
	if err != nil {
		problem(w, err)
		return
	}
	endpoint, err = (mcphosting.Config{Endpoint: subject.Policy.Endpoint}).URL(endpoint)
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	token := ""
	if len(tokenValues) == 1 {
		token = tokenValues[0]
	}
	var evidence api.MCPReadinessEvidence
	if role.Monitor.ManagedOAuth() {
		if len(tokenValues) > 0 {
			api.WriteProblem(w, api.ErrValidation("OAuth-bound readiness roles use managed credentials; omit the probe token header"))
			return
		}
		ref := role.Monitor.OAuthForScope(deployment.Scope)
		if ref == nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
		observer := mcpcatalog.ObserverRunner{Store: h.Store, Client: h.Client, Identities: h.Identities}
		credential, e := observer.AcquireReadinessOAuth(work, account.ID, app.ID, *ref, subject.Policy.Endpoint)
		if errors.Is(e, state.ErrMCPReadinessOAuthBusy) {
			problem(w, state.ErrConflict)
			return
		}
		if e != nil {
			reason := readinessOAuthFailure(e)
			evidence = api.MCPReadinessEvidence{Role: role.Name, ProtocolVersion: role.ProtocolVersion, CatalogFormat: role.CatalogFormat, Checks: []api.MCPReadinessCheck{{Name: "oauth_connection", Status: "failed", Detail: reason}}}
		} else {
			defer func(ctx context.Context) {
				release, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer stop()
				_ = credential.Release(release)
			}(work)
			evidence = probeEvidence(work, h.Client, endpoint, credential.Token, *role)
		}
	} else {
		evidence = probeEvidence(work, h.Client, endpoint, token, *role)
	}
	saved, err := store.RecordMCPReadiness(work, subject, evidence)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, saved)
}
func (h *Handler) Override(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := h.Store.(state.MCPReadinessStore)
	if !ok {
		problem(w, errors.New("store unavailable"))
		return
	}
	id, e := uuid.Parse(r.PathValue("id"))
	if e != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	deployment, lookupErr := h.Store.DeploymentByID(r.Context(), r.PathValue("id"))
	if errors.Is(lookupErr, state.ErrNotFound) {
		deployment, lookupErr = h.Store.DeploymentByID(r.Context(), strings.ReplaceAll(id.String(), "-", ""))
	}
	if lookupErr != nil {
		problem(w, state.ErrNotFound)
		return
	}
	app, lookupErr := h.Store.AppByID(r.Context(), deployment.AppID)
	if lookupErr != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, allowed := h.Loader.LoadApp(w, r, account, app.Slug); !allowed {
			return
		}
	}
	var request api.MCPReadinessOverrideRequest
	if !decode(w, r, &request) {
		return
	}
	saved, e := store.CreateMCPReadinessOverride(r.Context(), account.ID, deployment.ID, request)
	if e != nil {
		problem(w, e)
		return
	}
	httpkit.WriteJSON(w, 201, saved)
}
