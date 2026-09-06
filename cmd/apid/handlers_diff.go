// handlers_diff.go — POST /v1/apps/{slug}/diff (PR-1).
//
// Read-only preview of what a deploy would change without writing.
// Same engine the CLI uses (pkg/deploydiff); same wire shape the
// CLI emits under --json (api.DiffResponse). Bearer-key auth, no
// MFA, no DB writes, no audit row, no deployment row.
//
// Auth chain (registered in cmd/apid/server.go):
//   authLimited → requireScope(ScopesReadSurface...) → s.diffApp
//
// The read-only scopes mirror GET /v1/apps/{slug}/metrics (server.go:785)
// — a CI key with `apps:read` is sufficient. CI deploy keys
// typically carry `deploy:write`; a deploy-write key is also
// accepted (ScopeAdmin covers every read surface). The handler
// does NOT escalate to write paths; this is purely a compute.
//
// Phase order:
//  1. decodeJSON the DiffRequest body
//  2. Resolve plan limits (LimitsFor; ErrCapacity if unknown)
//  3. Load the app (loadApp: 404 on missing or cross-account)
//  4. Build the Baseline via pkg/state reads (LatestDeployment,
//     ListAllAppEnv, ListCronsForApp, ListEdgeRulesForApp)
//  5. Project the wire DiffRequest → deploydiff.Pending (15-line
//     adapter below)
//  6. Compute(slug, plan, baseline, pending) → Diff
//  7. Quota(plan, baseline, pending, QuotaConfig{…}) → append breaks
//  8. writeJSON 200 with Diff.ToWire() → DiffResponse
//
// App-not-found path: diff is a "what if" query, so we don't 404
// it. Baseline.App stays nil; the engine emits would-create-app
// Changes from the pointer-aware AppConfigPatch walk; the quota
// gate still fires against the customer's plan. The customer
// gets a 200 with blocking=true if the proposed fresh-app would
// breach quotas.
//
// Empty body (no AppConfig, no Manifest, no Env/Cron/EdgeRules):
// valid. Returns 200 with an empty Diff{Changes:[], Breaks:[]} and
// blocking=false (plan quota gate against a zero-value Pending
// never fires). Useful for the "preview current state" CI smoke
// test that diffs against the live deployment but proposes no
// changes.

package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// diffApp is the handler for POST /v1/apps/{slug}/diff. Registered
// in server.go next to the per-app metrics endpoint so the auth
// chain stays grouped.
func (s *server) diffApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.DiffRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}

	// Plan limits. The handler never reads pkg/api/limits.go
	// directly; it goes through LimitsFor which returns the
	// boolean the Quota gate needs to fire plan-feature branches
	// (streaming / jwt / ip / etc.).
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
		return
	}

	// App load. The diff is a "what if" query (the docstring at
	// the top of this file promises 200 + would-create-app Change
	// for fresh-app deploys), so we deliberately do NOT use
	// s.loadApp here — loadApp writes a 404 and the customer
	// loses the preview path. Read the store directly and
	// distinguish the two failure modes:
	//
	//   - not found          → continue with a zero-value app
	//                          (engine emits would-create-app
	//                          Changes; quota gate fires against
	//                          the would-be fresh app).
	//   - cross-account slug → 404 (IDOR protection must not be
	//                          weakened by the diff path).
	//
	// Defensive: if a non-NotFound error escapes AppBySlug, we
	// 500 via ErrCapacity — the customer sees a hard failure
	// rather than a misleading "would create" preview.
	slug := r.PathValue("slug")
	app, err := s.store.AppBySlug(r.Context(), slug)
	switch {
	case err == nil:
		// Found — enforce the IDOR boundary before the engine
		// sees the row.
		if app.AccountID != acct.ID {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"))
			return
		}
	case errors.Is(err, state.ErrNotFound):
		// Fresh-app preview path. Fall through with zero-value
		// app; buildDiffBaseline handles App == nil by emitting
		// would-create-app Changes from the AppConfigPatch walk.
		app = state.App{AccountID: acct.ID, Slug: slug}
	default:
		api.WriteProblem(w, api.ErrCapacity("could not load app"))
		return
	}

	// Build the baseline from pkg/state. App-found path:
	// populate App + LatestDeployment + env + crons + edge rules.
	// App-missing path: emit would-create-app Changes.
	baseline, err := s.buildDiffBaseline(r.Context(), app, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read baseline"))
		return
	}

	// Project wire → engine. ~15 lines, kept inline so the
	// adapter is obvious at the seam.
	pending := diffPendingFromRequest(&req)

	// Run the engine. Plan is set from acct.Plan (the customer
	// who called this endpoint), not from the baseline's app —
	// which has no plan field (dto.go).
	d := deploydiff.Compute(app.Slug, acct.Plan, baseline, pending)

	// Per-account cron count for the per-account quota branch
	// (CronLimitPerAccount). The CLI does the same via
	// accountCronCount (commands_diff.go:292-298); server-side
	// we read the store directly.
	accountCrons, err := s.store.ListCronsForAccount(r.Context(), acct.ID)
	if err != nil {
		// Don't fail the whole diff for a quota-gate read error —
		// skip the per-account branch and surface a warn so the
		// customer's eye lands on the gap.
		d.Breaks = append(d.Breaks, deploydiff.Break{
			Code:     "account_cron_count_unavailable",
			Severity: deploydiff.SeverityWarn,
			Reason:   "could not read per-account cron count; per-account cron quota gate skipped",
			Field:    "crons",
		})
		accountCrons = nil
	}
	breaks := deploydiff.Quota(acct.Plan, baseline, pending, deploydiff.QuotaConfig{
		Limits:               limits,
		AccountCronCount:     len(accountCrons),
		AccountEdgeRuleCount: 0, // per-account edge-rule count not
		// currently capped in pkg/api/limits.go; pass 0.
	})
	d.Breaks = append(d.Breaks, breaks...)

	// Wire conversion. ToWire sorts Changes by Field ASC and
	// Breaks by Code ASC with errors first — same order as the
	// CLI's --json, so a CI consumer reading either path agrees.
	writeJSON(w, http.StatusOK, d.ToWire())
}

// buildDiffBaseline reads the live state for the app into a
// [deploydiff.Baseline]. Mirrors the CLI's buildBaseline
// (commands_diff.go:156-220) but reads pkg/state directly
// instead of going through the SDK — the apid handler has the
// store in scope and avoids the SDK round-trip + pagination dance
// the CLI needs (ListDeployments is account-scoped on the SDK side;
// pkg/state.PgStore.ListDeploymentsForApp is app-scoped).
//
// Per-account ID is threaded through so the EnvByScope walk can
// filter out cross-account rows defensively (ListAllAppEnv takes
// (accountID, appID) and the SQL JOIN scopes on both).
func (s *server) buildDiffBaseline(ctx context.Context, app state.App, acct state.Account) (deploydiff.Baseline, error) {
	out := deploydiff.Baseline{
		App:        appResponsePtr(s.appResponse(app, acct.Plan)),
		EnvByScope: map[string][]string{},
	}
	// Latest deployment: single call (no pagination needed).
	dep, err := s.store.LatestDeployment(ctx, app.ID)
	if err == nil {
		// Convert state.Deployment → api.DeploymentResponse and
		// pin it on the baseline. Conversion helper exists at
		// s.deploymentResponse; reused so we don't drift from
		// the wire shape the CLI sees.
		dresp := s.deploymentResponse(dep, app)
		out.LatestDeployment = &dresp
		// SAFE-RELEASES production-leveling Stream E: pin the
		// scope of the latest deployment so the engine can emit
		// a `scope_mismatch` SeverityWarn break when the
		// pending deploy targets a different scope (cross-env
		// promotion). dep.Scope is already populated by the
		// SELECT projection pgstore loads (migrations/00213).
		out.LatestScope = dep.Scope
	} else if !errors.Is(err, state.ErrNotFound) {
		// Hard error — surface to the caller.
		return out, err
	}
	// Env vars per scope (ADR-090 D3 nested shape).
	envList, err := s.envListForApp(ctx, app.ID, acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return out, err
	}
	out.EnvByScope = deploydiff.EnvByScopeFromList(envList)
	// Crons.
	crons, err := s.store.ListCronsForApp(ctx, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return out, err
	}
	out.Crons = make([]api.CronResponse, 0, len(crons))
	for _, c := range crons {
		out.Crons = append(out.Crons, cronResponse(c))
	}
	// Edge rules.
	rules, err := s.store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return out, err
	}
	out.EdgeRules = make([]api.EdgeRuleResponse, 0, len(rules))
	for _, r := range rules {
		out.EdgeRules = append(out.EdgeRules, edgeRuleResponse(r))
	}
	return out, nil
}

// envListForApp reads the per-app env list and shapes it as the
// wire's AppEnvListResponse. The per-scope and __all__ query
// parameters from GET /v1/apps/{slug}/envs aren't part of the
// diff path — the engine's EnvByScopeFromList helper accepts
// either shape (nested or flat), so a default-scope read is
// sufficient for the baseline.
func (s *server) envListForApp(ctx context.Context, appID, accountID string) (api.AppEnvListResponse, error) {
	rows, err := s.store.ListAllAppEnv(ctx, accountID, appID)
	if err != nil {
		return api.AppEnvListResponse{}, err
	}
	out := api.AppEnvListResponse{
		Env:   make([]api.AppEnvResponse, 0, len(rows)),
		Quota: 0,
		Count: len(rows),
	}
	for _, r := range rows {
		out.Env = append(out.Env, api.AppEnvResponse{
			Key:       r.Key,
			CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			UpdatedAt: r.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}
	return out, nil
}

// diffPendingFromRequest projects the wire [api.DiffRequest] onto
// the engine's [deploydiff.Pending]. Kept inline so the type
// mapping is obvious; no behaviour of its own.
//
// AppConfigPatch → AppConfigPatch (field-for-field copy; both types
// share the same *T pointer semantics for nil-vs-explicit).
//
// Manifest: pass-through (api.AppManifest == engine's *api.AppManifest).
//
// ImageRef: string → string.
//
// EnvByScope: map[string][]DiffEnvRow → map[string][]PendingEnv
// (each DiffEnvRow holds plaintext so the quota gate's per-value
// byte cap can fire; the wire's list path never echoes values).
//
// Crons / EdgeRules: pass-through (api.CreateCronRequest /
// api.CreateEdgeRuleRequest are wire-stable per ADR-091).
func diffPendingFromRequest(req *api.DiffRequest) deploydiff.Pending {
	p := deploydiff.Pending{}
	if req.AppConfig != nil {
		p.AppConfig = deploydiff.AppConfigPatch{
			RAMMB:               req.AppConfig.RAMMB,
			CPUMillicores:       req.AppConfig.CPUMillicores,
			IdleTimeoutS:        req.AppConfig.IdleTimeoutS,
			MaxConcurrency:      req.AppConfig.MaxConcurrency,
			MinInstances:        req.AppConfig.MinInstances,
			EgressAllowlist:     req.AppConfig.EgressAllowlist,
			AutoscaleTargetRPS:  req.AppConfig.AutoscaleTargetRPS,
			AutoscaleTargetCP:   req.AppConfig.AutoscaleTargetCP,
			StreamingEnabled:    req.AppConfig.StreamingEnabled,
			WebSocketEnabled:    req.AppConfig.WebSocketEnabled,
			RequireSigned:       req.AppConfig.RequireSigned,
			WarmSnapshotEnabled: req.AppConfig.WarmSnapshotEnabled,
			RequireAuthn:        req.AppConfig.RequireAuthn,
			EvictionPriority:    req.AppConfig.EvictionPriority,
		}
	}
	p.Manifest = req.Manifest
	p.ImageRef = req.ImageRef
	// SAFE-RELEASES production-leveling Stream E: thread the
	// pending deployment's scope so the engine can emit a
	// `scope_mismatch` SeverityWarn break when the pending
	// deploy targets a different scope than the baseline
	// (cross-env promotion vs same-env patch). Empty string is
	// preserved verbatim — the engine treats "" as a no-op
	// (handler coerces "" → api.DefaultEnvScope at write time).
	p.Scope = req.Scope
	if len(req.EnvByScope) > 0 {
		p.EnvByScope = make(map[string][]deploydiff.PendingEnv, len(req.EnvByScope))
		for scope, rows := range req.EnvByScope {
			out := make([]deploydiff.PendingEnv, 0, len(rows))
			for _, r := range rows {
				out = append(out, deploydiff.PendingEnv{Key: r.Key, Value: r.Value})
			}
			p.EnvByScope[scope] = out
		}
	}
	if req.Crons != nil {
		p.Crons = append(p.Crons, req.Crons...)
	}
	if req.EdgeRules != nil {
		p.EdgeRules = append(p.EdgeRules, req.EdgeRules...)
	}
	return p
}

// appResponsePtr returns a pointer to the value so the
// [deploydiff.Baseline.App] field (which is *api.AppResponse) is
// wired through without an extra variable at every site.
func appResponsePtr(a api.AppResponse) *api.AppResponse {
	return &a
}
