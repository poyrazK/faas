// Package mcpcatalog owns opt-in catalog monitoring policy and observations.
package mcpcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func Handler(store state.Store, loader httpkit.AppLoader) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		app, loaded := loader.LoadApp(w, r, acct, r.PathValue("slug"))
		if !loaded {
			return
		}
		source, ok := store.(state.MCPCatalogMonitorStore)
		if !ok {
			respond(w, nil, errors.New("unsupported monitor store"))
			return
		}
		if r.Method == http.MethodGet {
			snapshot, err := source.GetMCPCatalogMonitor(r.Context(), acct.ID, app.ID)
			if err != nil {
				respond(w, nil, err)
				return
			}
			read(w, r, snapshot, app.Slug)
			return
		}
		if r.Method == http.MethodPut {
			configure(w, r, source, acct, app)
			return
		}
		if !app.AcceptsRequestInvocations() {
			api.WriteProblem(w, api.ErrValidation("catalog monitoring requires an HTTP app"))
			return
		}
		if !acct.Plan.CustomMetricsAllowed() {
			api.WriteProblem(w, api.ErrPlanCustomMetricsNotAllowed(acct.Plan))
			return
		}
		observe(w, r, source, acct.ID, app.ID)
	}
}
func read(w http.ResponseWriter, r *http.Request, snapshot api.MCPCatalogMonitorSnapshot, slug string) {
	if !api.ValidCatalogMonitorRole(r.URL.Query().Get("role")) {
		api.WriteProblem(w, api.ErrValidation("invalid monitor role"))
		return
	}
	if r.URL.Query().Get("all_roles") == "true" {
		if r.URL.Query().Get("evidence") != "" || r.URL.Query().Get("role") != "" {
			api.WriteProblem(w, api.ErrValidation("all_roles cannot be combined with role or evidence"))
			return
		}
		respond(w, api.CatalogMonitorSummary(snapshot, slug, time.Now().UTC()), nil)
		return
	}
	var exists bool
	snapshot, exists = api.CatalogMonitorRoleSnapshot(snapshot, r.URL.Query().Get("role"))
	if !exists {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if id := r.URL.Query().Get("evidence"); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("evidence must be a UUID"))
			return
		}
		for _, receipt := range snapshot.History {
			if receipt.ID == parsed.String() {
				respond(w, receipt, nil)
				return
			}
		}
		respond(w, nil, state.ErrNotFound)
		return
	}
	snapshot.State = api.CatalogMonitorSignal(snapshot, slug, time.Now().UTC()).State
	snapshot.OAuthLifecycle = api.CatalogOAuthLifecyclePublic(snapshot.OAuthLifecycle)
	snapshot.ObserverStatus = api.CatalogObserverStatus(snapshot, time.Now().UTC())
	respond(w, snapshot, nil)
}
func configure(w http.ResponseWriter, r *http.Request, source state.MCPCatalogMonitorStore, account state.Account, app state.App) {
	var req api.MCPCatalogMonitorConfigRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.MCPCatalogMonitorMaxRequestBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid catalog monitor configuration"))
		return
	}
	if !api.ValidCatalogMonitorRole(req.Role) || !api.ValidCatalogMonitorTokenEnv(req.TokenEnv) || api.ValidateScope(api.MCPConnectionScope(req.Scope)) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid monitor role or token environment name"))
		return
	}
	req.Role = api.CatalogMonitorRole(req.Role)
	if req.Enabled && !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrValidation("catalog monitoring requires an HTTP app"))
		return
	}
	if req.Enabled && !account.Plan.CustomMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanCustomMetricsNotAllowed(account.Plan))
		return
	}
	baseline, raw, err := contract(req.Baseline)
	if err != nil || baseline.Version != 2 {
		api.WriteProblem(w, api.ErrValidation("baseline must be a complete format-2 catalog contract"))
		return
	}
	if req.FreshnessSeconds == 0 {
		req.FreshnessSeconds = api.MCPCatalogMonitorDefaultFreshnessSeconds
	}
	if req.FreshnessSeconds < api.MCPCatalogMonitorMinFreshnessSeconds || req.FreshnessSeconds > api.MCPCatalogMonitorMaxFreshnessSeconds {
		api.WriteProblem(w, api.ErrValidation("freshness_seconds must be 15–3600"))
		return
	}
	if req.Enabled && req.Observer != nil && req.Observer.Enabled && app.PublicAuthMode == state.AppPublicAuthModeInternalOnly {
		api.WriteProblem(w, api.ErrValidation("managed observers require a public HTTP app origin"))
		return
	}
	if !validateObserverIntent(w, r, &req) {
		return
	}
	config := api.MCPCatalogMonitorConfig{Scope: req.Scope, Observer: req.Observer, Role: req.Role, TokenEnv: req.TokenEnv, Enabled: req.Enabled, Revision: uuid.NewString(), Baseline: raw, BaselineSHA256: digest(raw), FreshnessSeconds: req.FreshnessSeconds}
	out, err := source.ConfigureMCPCatalogMonitor(r.Context(), account.ID, app.ID, config)
	out.State = api.CatalogMonitorSignal(out, r.PathValue("slug"), time.Now().UTC()).State
	out.OAuthLifecycle = api.CatalogOAuthLifecyclePublic(out.OAuthLifecycle)
	out.ObserverStatus = api.CatalogObserverStatus(out, time.Now().UTC())
	respond(w, out, err)
}
func observe(w http.ResponseWriter, r *http.Request, source state.MCPCatalogMonitorStore, account, app string) {
	var req api.MCPCatalogObservationRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.MCPCatalogMonitorMaxRequestBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid catalog observation"))
		return
	}
	config, err := source.GetMCPCatalogMonitor(r.Context(), account, app)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !api.ValidCatalogMonitorRole(req.Role) {
		api.WriteProblem(w, api.ErrValidation("invalid monitor role"))
		return
	}
	req.Role = api.CatalogMonitorRole(req.Role)
	config, exists := api.CatalogMonitorRoleSnapshot(config, req.Role)
	if !exists {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if config.Config.Observer != nil && config.Config.Observer.Enabled {
		api.WriteProblem(w, api.ErrValidation("managed roles accept observations only from their platform observer"))
		return
	}
	if !config.Config.Enabled || req.Revision != config.Config.Revision {
		respond(w, nil, state.ErrMCPCatalogMonitorChanged)
		return
	}
	receipt, problem := evaluateObservation(config.Config, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	stored, err := source.RecordMCPCatalogObservation(r.Context(), account, app, receipt)
	respond(w, stored, err)
}
func contract(raw json.RawMessage) (mcphosting.Contract, []byte, error) {
	c, err := mcphosting.ReadContract(bytes.NewReader(raw))
	if err != nil {
		return c, nil, err
	}
	data, err := mcphosting.MarshalContract(c)
	return c, data, err
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func respond(w http.ResponseWriter, out any, err error) {
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Catalog monitor not found", "no matching monitor evidence belongs to this account"))
		return
	}
	if errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Catalog monitor changed", "reload the configured baseline revision before reporting"))
		return
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("invalid monitor configuration or role limit reached"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("catalog monitoring data unavailable"))
		return
	}
	httpkit.WriteJSON(w, 200, out)
}

func evaluateObservation(config api.MCPCatalogMonitorConfig, req api.MCPCatalogObservationRequest) (api.MCPCatalogReceipt, *api.Problem) {
	receipt := api.MCPCatalogReceipt{Role: api.CatalogMonitorRole(config.Role), ID: uuid.NewString(), Revision: req.Revision, State: "unavailable", BaselineSHA256: config.BaselineSHA256}
	if req.Unavailable {
		if len(req.Observed) != 0 {
			return receipt, api.ErrValidation("unavailable observations cannot contain a catalog")
		}
	} else {
		observed, raw, err := contract(req.Observed)
		if err != nil || observed.Version != 2 {
			return receipt, api.ErrValidation("observed must be a complete format-2 catalog contract")
		}
		baseline, _, err := contract(config.Baseline)
		if err != nil {
			return receipt, api.ErrCapacity("catalog comparison unavailable")
		}
		diff, err := mcphosting.CompareContractsWithOptions(baseline, observed, mcphosting.ContractDiffOptions{StrictCatalog: true})
		if err != nil {
			return receipt, api.ErrCapacity("catalog comparison unavailable")
		}
		receipt.ObservedSHA256 = digest(raw)
		receipt.State = "healthy"
		if receipt.ObservedSHA256 != receipt.BaselineSHA256 {
			receipt.State = "drift"
			if len(diff.Changes) == 0 {
				diff.Changes = append(diff.Changes, mcphosting.ContractChange{Path: "catalog", Kind: "changed", Severity: "needs_review"})
				diff.NeedsReview = true
			}
		}
		receipt.ChangedCount = len(diff.Changes)
		if len(diff.Changes) > api.MCPCatalogMonitorMaxChanges {
			diff.Changes = diff.Changes[:api.MCPCatalogMonitorMaxChanges]
			receipt.Truncated = true
		}
		receipt.Diff, _ = json.Marshal(diff)
		for len(receipt.Diff) > api.MCPCatalogMonitorMaxDiffBytes && len(diff.Changes) > 0 {
			diff.Changes = diff.Changes[:len(diff.Changes)-1]
			receipt.Truncated = true
			receipt.Diff, _ = json.Marshal(diff)
		}
	}
	return receipt, nil
}

func validateObserverIntent(w http.ResponseWriter, r *http.Request, req *api.MCPCatalogMonitorConfigRequest) bool {
	if req.Observer == nil {
		return true
	}
	if problem := req.Observer.NormalizeAndValidate(req.FreshnessSeconds, req.TokenEnv); problem != nil {
		api.WriteProblem(w, problem)
		return false
	}
	if req.Enabled && req.Observer.Enabled && !req.Observer.Paused && (req.Observer.SecretRef != "" || req.Observer.OAuth != nil) {
		principal, ok := httpkit.PrincipalFrom(r)
		if !ok || (principal.Key != nil && !httpkit.APIKeyCarriesScope(*principal.Key, api.ScopeAdmin) && !httpkit.APIKeyCarriesScope(*principal.Key, api.ScopeSecretsRead)) {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Secret access required", "managed credential binding requires secrets:read or admin scope"))
			return false
		}
	}
	return true
}
