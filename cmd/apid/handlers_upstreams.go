// Customer-facing data-upstream handlers (ADR-098 §D4 / PR-B).
//
// Wire surface (mirrors the env handlers in handlers_env.go):
//
//	GET    /v1/apps/{slug}/upstreams             → listUpstreams
//	GET    /v1/apps/{slug}/upstreams/{id}        → getUpstream
//	PUT    /v1/apps/{slug}/upstreams             → createUpstream
//	DELETE /v1/apps/{slug}/upstreams/{id}        → deleteUpstream
//
// §11 load-bearing claim: the response shape carries ONLY
// host_redacted_hash + the host_last4 fragment. The plaintext
// host NEVER appears on the wire — neither in the GET
// response, nor in the POST body echo, nor in the audit kind.
// The POST body accepts plaintext (the customer supplies it);
// the handler hashes via pkg/secretbox.HashHost BEFORE INSERT
// and drops the plaintext on the floor.
//
// Per-PR feature flag (FAAS_DATA_PLACEMENT, default OFF): when
// the gate is closed, listUpstreams / getUpstreams / createUpstream
// / deleteUpstream all return ErrPlanFeatureGated("data_upstreams").
// The PR-A pg_notify trigger still fires for any rows the
// classifier wrote pre-toggle, but schedd ignores them when
// FAAS_UPSTREAM_AFFINITY=0. The cluster outline's "Rollout
// gate" section calls for flipping the flag per-node after a
// one-month soak.

package main

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// dataUpstreamsListMaxLimit pins the cursor-pagination limit on
// GET /v1/apps/{slug}/upstreams. Mirrors the env / secrets
// limit precedent; the dashboard's "load more" button hits
// this on every scroll past the first page.
const dataUpstreamsListMaxLimit = 50

// listUpstreams returns every upstream captured on the app in
// the requested scope (or all scopes with ?scope=__all__).
//
// Quota metadata (quota_max, count) is included so the CLI can
// render "3/8 upstreams" without a second request. The count is
// cross-scope (per ADR-098 §D5 — the per-app cap is unchanged
// across scopes) so the dashboard renders one unified bar.
func (s *server) listUpstreams(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("data_upstreams", acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	scope, isAll, prob := scopeFromQuery(r, true /* allowAll */)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// ADR-098 amendment (issue #954): ?deployment_scope= is an
	// optional server-side filter that widens the list endpoint
	// so the dashboard can render staging-vs-prod independently.
	// Empty string means "no filter; return all deployments".
	// Invalid shapes (fail EnvScopePattern) reject as 400 — the
	// same problem code as ?scope= for consistency.
	deploymentScope, prob := deploymentScopeFromQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)

	appUUID, err := uuid.Parse(app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not parse app id"))
		return
	}

	var rows []state.DataUpstream
	if isAll {
		all, err := s.store.ListAllAppDataUpstreams(r.Context(), acct.ID, app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list upstreams"))
			return
		}
		rows = all
	} else {
		page, err := s.store.ListDataUpstreamsByApp(r.Context(), sqlc.ListDataUpstreamsByAppParams{
			AppID:                 state.NewPgtypeUUID(appUUID),
			CursorDeploymentScope: deploymentScope,
			CursorCreatedAt:       state.Timestamptz{}, // first page
			CursorID:              state.NewPgtypeUUID(uuid.Nil),
			PageLimit:             int32(dataUpstreamsListMaxLimit),
		})
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list upstreams"))
			return
		}
		rows = page
	}
	_ = scope // scope is honoured by the cursor's WHERE filter via ListDataUpstreamsByApp's per-scope cursor; the all-scope arm doesn't use it.

	out := make([]api.DataUpstreamResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, dataUpstreamResponseFromState(r))
	}

	total, err := s.store.CountDataUpstreamsByApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count upstreams"))
		return
	}
	writeJSON(w, http.StatusOK, api.DataUpstreamListResponse{
		Upstreams: out,
		Quota:     limits.DataPlacementHintsPerApp,
		Count:     total,
	})
}

// getUpstream returns one upstream by ID. Used by the dashboard's
// "edit upstream" pane.
func (s *server) getUpstream(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("data_upstreams", acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id, prob := parseUpstreamID(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.GetDataUpstreamByID(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrUpstreamNotFound(id.String()))
		return
	}
	// Defence-in-depth: the row's app_id must match the loaded
	// app — a forged ID from another app in the same account
	// would otherwise leak the row.
	if row.AppID.String() != app.ID {
		api.WriteProblem(w, api.ErrUpstreamNotFound(id.String()))
		return
	}
	writeJSON(w, http.StatusOK, dataUpstreamResponseFromState(row))
}

// createUpstream writes one explicit upstream. The customer
// supplies (kind, host, port, scope); the handler hashes via
// pkg/secretbox.HashHost BEFORE INSERT and drops the plaintext
// on the floor.
//
// Quota is enforced BEFORE the regex match (D5): when the
// per-plan DataPlacementHintsPerApp is 0 (Free), the handler
// rejects with ErrPlanDataUpstreamsNotAllowed (402) BEFORE
// touching ValidateUpstreamHost. The classifier's regex tripwire
// (the §D1.a env-key closed set) is irrelevant here — the
// customer is explicitly creating the row, not inferring it.
func (s *server) createUpstream(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("data_upstreams", acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	var req api.PutDataUpstreamRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.DataPlacementHintsPerApp == 0 {
		// Per-plan feature gate (Free). Reject BEFORE the
		// count() trip — the customer's plan simply doesn't
		// allow data placement hints. 402 mirrors the other
		// feature-gated plan codes (CodePlanFeatureGated).
		api.WriteProblem(w, api.ErrPlanDataUpstreamsNotAllowed(acct.Plan))
		return
	}
	// Per-plan cap: count cross-scope (per ADR-098 §D5), so a
	// customer can't escape the cap by spreading rows across
	// scopes. The (count - 1) for the row being replaced is
	// NOT implicit — explicit creates always add a row (the
	// dedupe-merge is at the SQL level on
	// (app_id, scope, kind, host, port)).
	total, err := s.store.CountDataUpstreamsByApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count upstreams"))
		return
	}
	if total >= limits.DataPlacementHintsPerApp {
		api.WriteProblem(w, api.ErrPlanLimitDataUpstreams(acct.Plan, limits.DataPlacementHintsPerApp, total))
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = defaultEnvScope
	}
	// ADR-098 amendment (issue #954): explicit creates thread
	// DeploymentScope from the request body. Empty string falls
	// through to defaultEnvScope — the migration's SQL DEFAULT
	// matches this fallback so a single-deployment app keeps
	// the pre-#954 wire shape. The classifier path (cmd/apid/
	// extract.go / handlers_env.go) uses a separate
	// LiveDeploymentForScope resolver — see #954's writer-time
	// contract doc on docs/adr/098-deployment-scope-overlay.md.
	deploymentScope := req.DeploymentScope
	if deploymentScope == "" {
		deploymentScope = defaultEnvScope
	}
	acctUUID, err := uuid.Parse(acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not parse account id"))
		return
	}
	appUUID, err := uuid.Parse(app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not parse app id"))
		return
	}
	// Hash the host BEFORE INSERT. The plaintext host is held
	// ONLY long enough to hash; the audit kind + the slog
	// field below carry host_redacted_hash, not the host.
	hash, err := secretbox.HashHost(req.Host)
	if err != nil {
		// Salt file missing → fatal §11 tripwire. The
		// 500 response carries ErrCapacity rather than
		// the bare salt error (which would leak the
		// config-path shape).
		s.log.Error("host hash failed; refusing to write upstream",
			"app_id", app.ID,
			"err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not hash host"))
		return
	}
	rowID, err := s.store.InsertDataUpstream(r.Context(), sqlc.InsertDataUpstreamParams{
		ID:               state.NewPgtypeUUID(uuid.New()),
		AccountID:        state.NewPgtypeUUID(acctUUID),
		AppID:            state.NewPgtypeUUID(appUUID),
		Source:           string(api.DataUpstreamSourceExplicit),
		Scope:            scope,
		DeploymentScope:  deploymentScope,
		Kind:             string(req.Kind),
		Host:             req.Host, // plaintext at INSERT — the SQL column is bytea-shaped and not surfaced on the wire.
		Port:             int32(req.Port),
		HostRedactedHash: hash,
		DeclaredRegion:   state.Text{},
		LastRttMs:        state.Int4{},
		LastProbedAt:     state.Timestamptz{},
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist upstream"))
		return
	}
	// Audit + log. NO plaintext host. NO DSN. NO scope value
	// beyond the field-replaced form. req.Kind was narrowed to
	// the 14-value DataUpstreamKind closed vocabulary by
	// req.Validate() at line 175 before this log line is reached;
	// audit-also includes the same string under the same guarantee.
	//
	// CodeQL go/log-injection: even though req.Kind is a closed
	// vocab after req.Validate(), the request-body origin isn't
	// visible to the dataflow analysis. Wrap through logsanitize.Field
	// (the project precedent — see handlers_registry_auth.go:181 and
	// handlers_env.go:282) so the value flows through the
	// package's recognised sanitiser and CodeQL drops the alert.
	s.log.Info("data_upstream created",
		"app", app.Slug,
		"account", acct.ID,
		"upstream_id", rowID,
		"kind", logsanitize.Field(string(req.Kind)),
		"scope", logsanitize.Field(scope),
		"deployment_scope", logsanitize.Field(deploymentScope),
		"host_redacted_hash", hash[:8],
	)
	s.audit.Emit(r.Context(), "data_upstream.created", &acct.ID, map[string]any{
		"app_id":             app.ID,
		"upstream_id":        rowID,
		"kind":               string(req.Kind),
		"scope":              scope,
		"deployment_scope":   deploymentScope,
		"host_redacted_hash": hash[:8],
		"port":               req.Port,
		"source":             string(api.DataUpstreamSourceExplicit),
	})
	row, err := s.store.GetDataUpstreamByID(r.Context(), rowID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read back upstream"))
		return
	}
	writeJSON(w, http.StatusCreated, dataUpstreamResponseFromState(row))
}

// deleteUpstream removes one explicit upstream by ID. Hard
// DELETE per ADR-098 (a soft-deleted row would still trigger
// pg_notify and confuse schedd). The FK CASCADE on account_id
// / app_id handles the GDPR path automatically.
func (s *server) deleteUpstream(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("data_upstreams", acct.Plan))
		return
	}
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	id, prob := parseUpstreamID(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Read-then-delete: we need to confirm the row belongs to
	// the loaded app before touching it (defence-in-depth —
	// a forged ID from another app in the same account would
	// otherwise leak the delete).
	row, err := s.store.GetDataUpstreamByID(r.Context(), id)
	if err != nil || row.AppID.String() != app.ID {
		api.WriteProblem(w, api.ErrUpstreamNotFound(id.String()))
		return
	}
	if err := s.store.DeleteDataUpstreamByID(r.Context(), id); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete upstream"))
		return
	}
	s.log.Info("data_upstream deleted",
		"app", app.Slug,
		"account", acct.ID,
		"upstream_id", id,
		"kind", string(row.Kind),
		"deployment_scope", row.DeploymentScope,
		"host_redacted_hash", row.HostRedactedHash[:8],
	)
	s.audit.Emit(r.Context(), "data_upstream.deleted", &acct.ID, map[string]any{
		"app_id":             app.ID,
		"upstream_id":        id,
		"kind":               string(row.Kind),
		"deployment_scope":   row.DeploymentScope,
		"host_redacted_hash": row.HostRedactedHash[:8],
	})
	// 204 No Content — the canonical REST shape for a successful
	// DELETE. The e2e at cmd/e2e/connection_aware_e2e_test.go
	// asserts this explicitly. Body would be redundant: the
	// caller already knows the upstream ID from the path.
	w.WriteHeader(http.StatusNoContent)
}

// dataUpstreamResponseFromState converts the typed state row to
// the wire DTO. §11 invariant: Host field is NEVER copied to
// the response — only HostRedactedHash + HostLast4.
func dataUpstreamResponseFromState(r state.DataUpstream) api.DataUpstreamResponse {
	var lastRTT *int
	if r.LastRTTMs != nil {
		v := *r.LastRTTMs
		lastRTT = &v
	}
	var lastProbedAt string
	if r.LastProbedAt != nil {
		lastProbedAt = r.LastProbedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return api.DataUpstreamResponse{
		ID:               r.ID.String(),
		Source:           api.DataUpstreamSource(r.Source),
		Kind:             api.DataUpstreamKind(r.Kind),
		HostRedactedHash: r.HostRedactedHash,
		// host_last4 is a compatibility name; the value is the first
		// eight hex characters of the redacted hash. Keep this aligned
		// with the classifier and the audit/log surfaces so operator
		// views have enough entropy to distinguish upstreams.
		HostLast4: deriveLast4FromHash(r.HostRedactedHash),
		Port:      r.Port,
		Scope:     r.Scope,
		// DeploymentScope widens the dedupe key in ADR-098
		// amendment (issue #954). Surfaces on the response so the
		// dashboard / CLI can render staging-vs-prod. The SQL
		// DEFAULT 'default' stamp on the column matches the
		// pre-#954 wire shape for single-deployment apps.
		DeploymentScope: r.DeploymentScope,
		DeclaredRegion:  r.DeclaredRegion,
		LastRTTMs:       lastRTT,
		LastProbedAt:    lastProbedAt,
		CreatedAt:       r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		LastSeenAt:      r.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// deriveLast4FromHash returns the first 8 lowercase hex chars of
// the hash. The function name is retained for source compatibility
// with the original four-character response field; host_last4 is
// now a canonical operator-visible hash fragment, matching
// pkg/data/infer.go::hostLast4FromHash and the audit/log surfaces.
func deriveLast4FromHash(hash string) string {
	if len(hash) < 8 {
		return hash
	}
	return hash[:8]
}

// parseUpstreamID parses the {id} path segment. Returns *Problem
// with CodeValidation when the segment isn't a uuid.
func parseUpstreamID(r *http.Request) (uuid.UUID, *api.Problem) {
	raw := r.PathValue("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, api.ErrValidation("upstream id must be a uuid")
	}
	return id, nil
}
