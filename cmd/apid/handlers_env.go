// handlers_env.go — apid handlers for customer env vars
// (issue #395 / ADR-045 / ADR-090).
//
// Routes (registered in server.go::handler):
//
//	GET    /v1/apps/{slug}/env          → listEnv
//	PUT    /v1/apps/{slug}/env/{key}    → setEnv
//	DELETE /v1/apps/{slug}/env/{key}    → deleteEnv
//
// All three routes accept an optional `?scope=` query param
// (ADR-090 D2 / PR-B). The scope is a domain-valid slug
// (3..40 lowercase alnum + dash, see api.EnvScopePattern) or
// the reserved sentinel `__all__` on GET only. Omitted `?scope=`
// means `scope=default` — the wire shape is byte-identical to
// pre-PR-B. See api.ValidateScope for the rejection rules.
//
// Trust model
//
//   - Plaintext VALUES arrive over TLS via PUT body and live transiently
//     in this handler. There is NO seal step — values land in app_envs as
//     plaintext TEXT, by contract (issue #395 plaintext rationale +
//     ADR-045 §Decision). The values are non-sensitive runtime config
//     (LOG_LEVEL, FEATURE_X, ...); credentials stay on /secrets.
//   - No log line ever contains the plaintext VALUE. Key names are public
//     per spec §11 and flow freely.
//   - Scopes: GET requires env:read (or admin); PUT/DELETE require
//     env:write (or admin). env:write is NOT MFA-gated because env vars
//     are explicitly non-sensitive (ADR-045 §Decision — see the
//     credentials-only MFA rationale in handlers_secrets.go).

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/data"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// stdctxEnv alias mirrors handlers_secrets.go so the file is self-
// contained and the helper signatures read cleanly.
type stdctxEnv = context.Context

// defaultEnvScope is the scope name stamped on rows that pre-date
// ADR-090 (created before migration 00203 added the `scope` column)
// and on writes that omit `?scope=`. The constant is reused at
// every read/write call site so a future rename (e.g. to "prod") is
// a one-line change. See ADR-090 D1 — pre-00203 rows get
// scope='default' via the PG11+ fast-default backfill in the
// migration's `NOT NULL DEFAULT 'default'` clause.
const defaultEnvScope = "default"

// scopeFromQuery resolves the optional `?scope=` query param. Empty
// means "use the default scope" — the same wire shape pre-PR-B
// customers rely on. The reserved sentinel "__all__" is allowed on
// GET only (rejected as ErrEnvScopeReserved on PUT/DELETE); any
// other malformed value is rejected as ErrEnvScopeInvalid. Returns
// (scope, isAll, problem) so listEnv can branch on isAll without
// re-parsing the query string.
//
// scopeQueryParam is the wire-level query-string key for the env
// scope. The gregale CLI and the SDK generator hard-code the
// `scope` literal; if you rename it, also update the `?scope=`
// references in api/openapi.yaml and the SDK.
const scopeQueryParam = "scope"

func scopeFromQuery(r *http.Request, allowAll bool) (scope string, isAll bool, prob *api.Problem) {
	raw := r.URL.Query().Get(scopeQueryParam)
	if raw == "" {
		return defaultEnvScope, false, nil
	}
	if raw == api.EnvScopeAllSentinel {
		if !allowAll {
			return "", false, api.ErrEnvScopeReserved(api.EnvScopeAllSentinel)
		}
		return "", true, nil
	}
	if prob := api.ValidateScope(raw); prob != nil {
		return "", false, prob
	}
	return raw, false, nil
}

// deploymentScopeFromQuery resolves the optional `?deployment_scope=`
// query param (ADR-098 amendment, issue #954). Empty string is the
// "no filter; return all deployments" form — same wire shape as the
// pre-#954 list endpoint. Any non-empty value must match
// api.EnvScopePattern (the regex mirrors migration 00281's CHECK
// data_upstreams_deployment_scope_shape). Malformed values reject
// as ErrEnvScopeInvalid so the dashboard renders one consistent
// problem card with the env-scope 400 path.
//
// Unlike scopeFromQuery, there is no "__all__" sentinel — the
// deployment-scope filter is opt-in only; the default empty form
// already covers the "all deployments" case via the SQL filter
// (sqlc.arg cursor_deployment_scope IS NULL OR =” → no filter).
//
// deploymentScopeQueryParam is the wire-level query-string key
// for the deployment scope. The gregale CLI / SDK generator
// hard-code the `deployment_scope` literal — rename carefully.
const deploymentScopeQueryParam = "deployment_scope"

func deploymentScopeFromQuery(r *http.Request) (string, *api.Problem) {
	raw := r.URL.Query().Get(deploymentScopeQueryParam)
	if raw == "" {
		return "", nil
	}
	if len(raw) > api.MaxEnvScopeLen || !envScopeReForDeployment.MatchString(raw) {
		return "", api.ErrEnvScopeInvalid("deployment_scope must be 3..40 chars, lowercase alnum + dash")
	}
	return raw, nil
}

// envScopeReForDeployment is the compiled EnvScopePattern regex,
// pre-compiled once for the deployment-scope query param. Uses
// the same shape as envScopeRe (env-scope validator); kept as a
// distinct symbol so the deployment-scope path stays
// independently audited (the validator's "scope is required"
// rejection would otherwise bleed into the optional filter).
var envScopeReForDeployment = regexp.MustCompile(api.EnvScopePattern)

// listEnv returns every env var on the app in the requested scope.
// The VALUE never appears in the GET response (guest-init reads the
// value from /etc/faas/env.json at process start). Quota info is
// included so the CLI can show "3/8 env vars" without a separate
// call.
//
// `?scope=__all__` returns a nested `env_by_scope` map shape
// (ADR-090 D3); the flat `env` array is empty. All other scopes
// return the flat `env` array as before. The count + quota fields
// count across ALL scopes (the per-app EnvVarsMax is unchanged
// across scopes per ADR-090 D6) so the CLI can render a unified
// progress bar.
func (s *server) listEnv(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	limits := api.MustLimitsFor(acct.Plan)

	if isAll {
		// `?scope=__all__` returns the nested map shape. Read every
		// row on the app via the scope-agnostic store path. The
		// all-scope read is rare (operator-only) and the per-app
		// row count is capped at Limits.EnvVarsMax.
		rows, err := s.store.ListAllAppEnv(r.Context(), acct.ID, app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list env vars"))
			return
		}
		writeEnvListAll(w, rows, limits.EnvVarsMax)
		return
	}

	rows, err := s.store.ListAppEnvInScope(r.Context(), acct.ID, app.ID, scope)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list env vars"))
		return
	}
	out := make([]api.AppEnvResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.AppEnvResponse{
			Key:       row.Key,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	// Count across ALL scopes so the dashboard renders one unified
	// "N / EnvVarsMax" bar regardless of which scope the customer
	// is currently inspecting. Per-ADR-090 D6 the per-app quota is
	// unchanged across scopes — a customer's "staging" rows count
	// toward the same EnvVarsMax as their "default" rows.
	totalCount, err := s.store.CountAppEnv(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count env vars"))
		return
	}
	writeJSON(w, http.StatusOK, api.AppEnvListResponse{
		Env:   out,
		Quota: limits.EnvVarsMax,
		Count: totalCount,
	})
}

// writeEnvListAll renders the nested `env_by_scope` response shape
// for `?scope=__all__`. The flat `env` array is empty; the map
// keys are scope names. Rows are grouped by scope and the per-scope
// slice is sorted by key ASC to match the flat response. Count is
// the row total (cross-scope); Quota is unchanged.
func writeEnvListAll(w http.ResponseWriter, rows []state.AppEnv, quota int) {
	// Bucket rows by scope. Deterministic ordering: scope ASC, then
	// per-scope key ASC — matches the flat response's key ASC for
	// each scope, and the per-scope list in the nested response.
	bucket := map[string][]api.ScopedAppEnvResponse{}
	for _, r := range rows {
		bucket[r.Scope] = append(bucket[r.Scope], api.ScopedAppEnvResponse{
			Scope:     r.Scope,
			Key:       r.Key,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	for scope := range bucket {
		sort.Slice(bucket[scope], func(i, j int) bool {
			return bucket[scope][i].Key < bucket[scope][j].Key
		})
	}
	// Render the map in scope-ASC order so the wire output is
	// stable across calls (Go's map iteration is randomized).
	scopes := make([]string, 0, len(bucket))
	for scope := range bucket {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	ordered := make(api.EnvByScope, len(bucket))
	for _, scope := range scopes {
		ordered[scope] = bucket[scope]
	}
	writeJSON(w, http.StatusOK, api.AppEnvListResponse{
		Env:        nil, // discriminated union: empty in the __all__ arm
		EnvByScope: ordered,
		Quota:      quota,
		Count:      len(rows),
	})
}

// setEnv upserts the (app_id, scope, key) row with the plaintext
// VALUE. Quota is enforced before the persist so an over-cap
// request is rejected without touching the store. Idempotent:
// re-PUT replaces value + bumps updated_at.
//
// The `?scope=` query param selects which scope to write into; the
// reserved sentinel `__all__` is rejected (400 env_scope_reserved)
// because it has no meaning on a single-row write. Omitted scope
// means `scope=default` — pre-PR-B callers see no behaviour change.
//
// Hand-rolled phases (validate key → resolve app → validate body →
// validate scope → check quota → persist → log → audit), not a
// helper, because the line budget is well under the §Conventions
// 50-line cap and the phase order matters for auditing.
func (s *server) setEnv(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	key := r.PathValue("key")
	if prob := api.ValidateEnvKey(key); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	scope, _, prob := scopeFromQuery(r, false /* allowAll */)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var req api.PutAppEnvRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if prob := req.Validate(limits.EnvValueMaxBytes); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.checkEnvQuota(r.Context(), acct, app, scope, key, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if err := s.store.UpsertAppEnvInScope(r.Context(), acct.ID, app.ID, scope, key, req.Value); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist env var"))
		return
	}
	// A snapshot contains the guest process environment in its memory image.
	// Invalidate every non-stale snapshot before acknowledging the mutation so
	// the next wake cold-boots with the new value instead of restoring an old
	// process image. Running instances remain unchanged; live reload is an
	// explicit opt-in application feature, not an OS environment mutation.
	invalidated, err := s.invalidateAppSnapshots(r.Context(), app.ID)
	if err != nil {
		s.log.Error("env set: invalidate snapshots", "app", app.Slug, "err", err)
		s.audit.Emit(r.Context(), "env.snapshot_invalidation_failed", &acct.ID, map[string]any{
			"app_id": app.ID, "scope": scope, "name": key,
		})
		api.WriteProblem(w, api.ErrCapacity("could not invalidate application snapshots"))
		return
	}
	// ADR-098 PR-B (C4): when the data-placement flag is on,
	// run the env-classifier on the just-persisted row. The
	// classifier (pkg/data/infer.go) extracts host + port from
	// the value, hashes the host via pkg/secretbox.HashHost,
	// and upserts a data_upstreams row. The plaintext host is
	// dropped on the floor — only the hash + kind + port land
	// in the table (§11 invariant).
	//
	// The classifier + insert path runs synchronously here
	// because the env mutation is the customer's synchronisation
	// point: they're waiting for the response. A reload-style
	// async pipeline would race the customer's next request
	// (the e2e at cmd/e2e/connection_aware_e2e_test.go
	// asserts the row exists immediately after the env PUT).
	// The classifier is cheap (one env row + one dedupe-merge
	// INSERT) so the latency hit is sub-ms.
	if s.runtimeBool(runtimeConfigDataPlacement, s.dataPlacementEnabled) {
		if err := s.runEnvClassifier(r.Context(), acct, app, scope, key, req.Value); err != nil {
			// The env row is already persisted — a classifier
			// failure (e.g. secretbox salt missing) is a
			// data-plane concern, NOT a request failure.
			// Log and continue; the next env mutation will
			// re-attempt the classifier, and the importer
			// path can backfill. CRITICAL: this branch
			// returns 200 to the customer — partial success
			// is the right product behaviour.
			s.log.Warn("env-classifier failed after env upsert",
				"app", app.Slug,
				"scope", logsanitize.Field(scope),
				"key", logsanitize.Field(key),
				"account", acct.ID,
				"err", err.Error(),
			)
			// Issue #957: SOC 2 CC7.2 wants a traceable
			// record of the failure mode independent of the
			// operator log. The audit row carries a
			// closed-vocab error_class discriminator so an
			// operator can triage without log-spelunking.
			// silent_skip=true means the classifier dropped
			// the row on the floor (host_hash_failed);
			// silent_skip=false means we got far enough to
			// attempt an insert that failed. The
			// underlying err.Error() is intentionally NOT
			// surfaced on the audit row (see
			// errEnvClassifier doc on handlers_env.go
			// above the sentinel type).
			errorClass := errClassifierInternal.Kind
			silentSkip := false
			var ec *errEnvClassifier
			if errors.As(err, &ec) {
				errorClass = ec.Kind
				// silent_skip is true iff the failure bailed
				// before any data_upstreams INSERT was
				// attempted. Only `insert_data_upstream` is
				// the post-INSERT failure (the INSERT ran,
				// collided, and we observed the error); every
				// other Kind fails before reaching
				// InsertDataUpstream at runEnvClassifier
				// (uuid_parse at L570/L574, classifier.Run
				// at L610, CountDataUpstreamsByApp at L634,
				// HostHashOK at L624, port bounds at L666).
				silentSkip = ec.Kind != errClassifierInsert.Kind
			}
			s.audit.Emit(r.Context(), "env.classifier_failed", &acct.ID, map[string]any{
				"app_id":      app.ID,
				"scope":       scope,
				"name":        key,
				"error_class": errorClass,
				"silent_skip": silentSkip,
			})
		}
	}
	// Audit + log. VALUE never reaches slog. logsanitize.RedactValue is
	// used defensively even though we never log req.Value directly — a
	// future refactor that adds a "request echo" log line won't leak.
	s.log.Info("env set",
		"app", app.Slug,
		"scope", logsanitize.Field(scope),
		"key", logsanitize.Field(key),
		"account", acct.ID,
		"value_bytes", logsanitize.RedactValue(req.Value),
	)
	// Issue #395: env.set is distinct from secret.set in the audit
	// taxonomy so the secret-quota bypass argument is closed — a
	// customer can't hide credentials under the env var surface
	// without losing the audit-kind signal that says "config change"
	// vs "credential change". Same posture as slog: no plaintext.
	//
	// ADR-090 PR-B: the audit payload widens to include `scope` so
	// downstream consumers (SIEM, dashboard filter) can attribute
	// the change to a specific environment. `scope="default"` is
	// the pre-PR-B shape byte-for-byte — this is purely additive.
	s.audit.Emit(r.Context(), "env.set", &acct.ID, map[string]any{
		"app_id":                app.ID,
		"scope":                 scope,
		"name":                  key,
		"snapshots_invalidated": invalidated,
	})
	writeJSON(w, http.StatusOK, struct {
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}{Key: key, Scope: scope})
}

// checkEnvQuota returns nil when a PUT for (app, scope, key) is
// allowed under the per-plan EnvVarsMax, or a ready-to-write
// *api.Problem otherwise. Re-PUTs of an existing (scope, key) are
// not new rows and so don't count against the quota — the
// (count - 1) for the row being replaced is implicit.
//
// Scope is part of the quota path: a customer's "staging" rows
// count toward the same EnvVarsMax as their "default" rows per
// ADR-090 D6. The quota is unchanged across scopes.
//
// Shape mirrors checkSecretQuota exactly so a future refactor that
// drops one or the other (e.g. special-casing env vs secret) trips
// this seam.
func (s *server) checkEnvQuota(c stdctxEnv, acct state.Account, app state.App, scope, key string, limits api.Limits) *api.Problem {
	// Count across ALL scopes — the per-app EnvVarsMax is global
	// per ADR-090 D6, so a customer moving keys between scopes
	// (write-then-delete) is bounded by the same cap.
	n, err := s.store.CountAppEnv(c, acct.ID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not count env vars")
	}
	already, err := s.envExistsInScope(c, acct.ID, app.ID, scope, key)
	if err != nil {
		return api.ErrCapacity("could not check env var")
	}
	if !already && n >= limits.EnvVarsMax {
		return api.ErrPlanLimitEnvVars(limits, n)
	}
	return nil
}

// deleteEnv removes the (app_id, scope, key) row. Two distinct
// failure modes map to two distinct status codes — both are
// documented on the OpenAPI DELETE shape
// (api/openapi.yaml:1509-1513):
//
//   - loadApp misses        → 404 NotFound (the *app* doesn't exist
//     or isn't owned by the caller; loadApp owns this contract)
//   - DeleteAppEnv misses   → 400 CodeEnvVarNotFound (the env var
//     key isn't set on an existing app; the URL resource IS the
//     env var, not the app)
//
// The 400/404 split mirrors the secrets DELETE surface
// (handlers_secrets.go + api/openapi.yaml:1429-1438) so SDK
// callers reuse the same error-decoding branch. The DELETE is
// idempotent on the row side: a re-DELETE of a just-removed row
// returns 400 env_var_not_found, not 404 — by design.
//
// `?scope=__all__` is rejected (400 env_scope_reserved) — same
// reason as on PUT: the sentinel has no meaning on a single-row
// delete. Omitted scope means `scope=default`.
func (s *server) deleteEnv(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	key := r.PathValue("key")
	if prob := api.ValidateEnvKey(key); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	scope, _, prob := scopeFromQuery(r, false /* allowAll */)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if err := s.store.DeleteAppEnvInScope(r.Context(), acct.ID, app.ID, scope, key); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrEnvVarNotFound(key))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete env var"))
		return
	}
	invalidated, err := s.invalidateAppSnapshots(r.Context(), app.ID)
	if err != nil {
		s.log.Error("env delete: invalidate snapshots", "app", app.Slug, "err", err)
		s.audit.Emit(r.Context(), "env.snapshot_invalidation_failed", &acct.ID, map[string]any{
			"app_id": app.ID, "scope": scope, "name": key,
		})
		api.WriteProblem(w, api.ErrCapacity("could not invalidate application snapshots"))
		return
	}
	s.log.Info("env deleted",
		"app", app.Slug,
		"scope", logsanitize.Field(scope),
		"key", logsanitize.Field(key),
		"account", acct.ID,
	)
	// Issue #395: env.deleted is the DELETE counterpart of env.set.
	// ADR-090 PR-B: payload widens with `scope` (same shape as
	// env.set). Pre-PR-B audit consumers see an extra field but
	// no semantic change to the existing ones.
	s.audit.Emit(r.Context(), "env.deleted", &acct.ID, map[string]any{
		"app_id":                app.ID,
		"scope":                 scope,
		"name":                  key,
		"snapshots_invalidated": invalidated,
	})
	w.WriteHeader(http.StatusNoContent)
}

// invalidateAppSnapshots marks all currently restorable snapshots for every
// deployment of the app stale. Snapshot rows are a cache of a booted process,
// and an app may have more than one live deployment during a traffic split or
// rollback window. Invalidating only the newest deployment could therefore
// resurrect an older process carrying the previous environment. Repeated
// LatestSnapshotForTier calls are intentional: the state.Store interface
// exposes the latest-row primitive, and each successful mark removes that row
// from the next lookup.
func (s *server) invalidateAppSnapshots(ctx context.Context, appID string) (int, error) {
	deployments, err := s.store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if errors.Is(err, state.ErrNotFound) {
		// Apps without a deployment have no snapshot cache to invalidate.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list deployments: %w", err)
	}
	invalidated := 0
	for _, deployment := range deployments {
		for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
			for {
				snap, snapErr := s.store.LatestSnapshotForTier(ctx, deployment.ID, tier)
				if errors.Is(snapErr, state.ErrNotFound) {
					break
				}
				if snapErr != nil {
					return invalidated, fmt.Errorf("latest %s snapshot for deployment %s: %w", tier, deployment.ID, snapErr)
				}
				if snap.ID == "" {
					break
				}
				markErr := s.store.MarkSnapshotStale(ctx, snap.ID)
				if markErr != nil && !errors.Is(markErr, state.ErrNotFound) {
					return invalidated, fmt.Errorf("mark %s snapshot %s stale: %w", tier, snap.ID, markErr)
				}
				if markErr == nil {
					invalidated++
				} else {
					// A concurrent park/GC may remove the row between the
					// latest lookup and this update. It is already no longer
					// restorable; stop this tier's walk to avoid retrying a
					// row whose state changed underneath us.
					break
				}
			}
		}
	}
	return invalidated, nil
}

// envExistsInScope checks if a (app_id, scope, key) row exists for
// the account. Used by setEnv to subtract 1 from the quota count
// when an upsert is replacing an existing row. Mirrors secretExists
// — see that helper's comment for the O(n) rationale and the
// Store-surface decision.
func (s *server) envExistsInScope(c stdctxEnv, accountID, appID, scope, key string) (bool, error) {
	rows, err := s.store.ListAppEnvInScope(c, accountID, appID, scope)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.Key == key {
			return true, nil
		}
	}
	return false, nil
}

// errEnvClassifier is the typed error wrapper returned from
// runEnvClassifier. The Kind field is the closed-vocab discriminator
// written to the env.classifier_failed audit row (issue #957) so an
// operator can triage the failure mode without log-spelunking. The
// underlying Err is intentionally NOT surfaced on the audit row —
// secretbox.HostHashSaltError can carry salt-path material, and
// future error classes could carry plaintext-adjacent context. SOC 2
// CC6.1 confidential logging. The discriminator is sufficient.
//
// Use errors.As(err, &ec) to unwrap; ec.Kind is the audit payload's
// `error_class` value.
type errEnvClassifier struct {
	Kind string
	Err  error
}

func (e *errEnvClassifier) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return "env-classifier: " + e.Kind
	}
	return "env-classifier: " + e.Kind + ": " + e.Err.Error()
}

func (e *errEnvClassifier) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Closed-vocab sentinels for the audit row's `error_class` value.
// Any new failure site in runEnvClassifier MUST wrap with one of these
// (or errClassifierInternal as the catch-all default).
var (
	errClassifierUUIDParse      = &errEnvClassifier{Kind: "uuid_parse"}
	errClassifierHostHashFailed = &errEnvClassifier{Kind: "host_hash_failed"}
	errClassifierInsert         = &errEnvClassifier{Kind: "insert_data_upstream"}
	errClassifierPortRange      = &errEnvClassifier{Kind: "port_out_of_range"}
	errClassifierInternal       = &errEnvClassifier{Kind: "classifier_internal"}
)

// runEnvClassifier (ADR-098 PR-B / C4 + amendment issue #954) ingests
// one env mutation through the env-classifier and upserts the resulting
// rows into data_upstreams. The classifier (pkg/data/infer.go) walks
// the env row, extracts host + port from the value, hashes the host
// via pkg/secretbox.HashHost, and shapes the InferredUpstream rows.
// The insert uses the InsertDataUpstream path (dedupe-merge on
// (account_id, app_id, scope, deployment_scope, kind, host_redacted_hash,
// port)) so a re-PUT of the same DSN is idempotent.
//
// §11 invariant: the env plaintext reaches the helper ONLY via
// the value arg; the classifier hashes the host, the helper
// inserts only the hash, the plaintext host is dropped on the
// floor before this function returns.
//
// ADR-098 amendment (issue #954): every inferred row stamps
// DeploymentScope via s.deploymentScopeForEnvRow (resolves the
// live deployment targeting the env-scope via the existing
// pgstore.LiveDeploymentForScope shim; falls back to
// defaultEnvScope="default" on ErrNotFound or empty scope).
// This is the writer-time contract from
// docs/adr/098-deployment-scope-overlay.md §D3.
//
// Returns the classifier error verbatim — the caller (setEnv)
// logs + continues (the env row is already persisted; the
// classifier is a best-effort data-plane concern).
func (s *server) runEnvClassifier(ctx context.Context, acct state.Account, app state.App, scope, key, value string) error {
	acctUUID, err := uuid.Parse(acct.ID)
	if err != nil {
		return &errEnvClassifier{Kind: errClassifierUUIDParse.Kind, Err: fmt.Errorf("parse account uuid: %w", err)}
	}
	appUUID, err := uuid.Parse(app.ID)
	if err != nil {
		return &errEnvClassifier{Kind: errClassifierUUIDParse.Kind, Err: fmt.Errorf("parse app uuid: %w", err)}
	}
	// Resolve the deployment ONCE per classifier run. The env-
	// scope is fixed for the whole run (one mutation, one scope),
	// so a single resolver call covers every inferred row.
	deploymentScope, err := s.deploymentScopeForEnvRow(ctx, appUUID, scope)
	if err != nil {
		// Fail-open on resolver errors: log + fall back to the
		// migration's DEFAULT 'default' stamp. The classifier
		// is a best-effort data-plane concern; a transient
		// LiveDeploymentForScope hiccup must not break env
		// mutations.
		s.log.Warn("deploymentScopeForEnvRow resolver failed; falling back to default scope",
			"app_id", app.ID,
			"env_scope", logsanitize.Field(scope),
			"err", err)
		deploymentScope = defaultEnvScope
	}
	classifier := data.NewClassifier(s.log, app.ID)
	// hostHashFunc is the test seam (issue #957 / cmd/apid/server.go).
	// Production wiring (cmd/apid/main.go) does not call
	// WithHostHashFunc, so s.hostHashFunc stays nil and we fall
	// through to the canonical secretbox.HashHost — byte-for-byte
	// pre-PR-C behaviour.
	if s.hostHashFunc != nil {
		classifier.HashHost = s.hostHashFunc
	} else {
		classifier.HashHost = secretbox.HashHost
	}
	classifier.ResolveDefaultPort = func(kind string) (int, bool) {
		return api.DefaultPortForKind(api.DataUpstreamKind(kind))
	}
	result, err := classifier.Run(ctx, []data.EnvRow{
		{Key: key, Value: value, Scope: scope},
	})
	if err != nil {
		return &errEnvClassifier{Kind: errClassifierInternal.Kind, Err: err}
	}
	for _, row := range result.Rows {
		if !row.HostHashOK {
			// HashHost failed (e.g. salt missing). The
			// data_upstreams schema enforces NOT NULL on
			// host_redacted_hash, so an INSERT with the
			// empty hash would trip 23502. Issue #957:
			// instead of a silent `continue` (which
			// leaves no SOC 2 trace), return a typed
			// sentinel so setEnv emits
			// env.classifier_failed with
			// silent_skip=true. The env row IS already
			// persisted — the customer still gets 200.
			return errClassifierHostHashFailed
		}
		// Per-plan quota check (DataPlacementHintsPerApp).
		// The classifier returns ALL rows that match the
		// current env mutation; the total count against
		// the per-app cap is what matters. Mirror the
		// createUpstream path's per-plan check.
		limits := api.MustLimitsFor(acct.Plan)
		current, err := s.store.CountDataUpstreamsByApp(ctx, acct.ID, app.ID)
		if err != nil {
			return &errEnvClassifier{Kind: errClassifierInternal.Kind, Err: err}
		}
		if limits.DataPlacementHintsPerApp > 0 && current >= limits.DataPlacementHintsPerApp {
			// Quota reached — log + skip. The Free-plan
			// short-circuit (0/3/10/50) is handled here:
			// Free apps have DataPlacementHintsPerApp=0
			// so the classifier never inserts.
			s.log.Warn("env-classifier quota reached",
				"app", app.Slug,
				"account", acct.ID,
				"kind", row.Kind,
				"current", current,
				"limit", limits.DataPlacementHintsPerApp,
			)
			continue
		}
		hash := row.HostHash
		// Inferred rows carry the plaintext host in
		// row.Host ONLY for the duration of the
		// classifier walk. The INSERT below passes the
		// host into the col that's then never surfaced
		// on the wire (the SQL column is bytea-shaped
		// and not in the GET response). The hash is
		// the only on-wire identifier.
		// Clamp row.Port into the sqlc int32 range. The schema
		// CHECK constraint in migration 00226 pins port to
		// BETWEEN 1 AND 65535 (well within int32), so the
		// narrow is safe-by-construction. The explicit
		// round-trip check below makes the bound visible to
		// CodeQL's `go/incorrect-integer-conversion` query.
		var port32 int32
		if row.Port < 0 || row.Port > 65535 {
			return &errEnvClassifier{Kind: errClassifierPortRange.Kind, Err: fmt.Errorf("apid: inferred port %d out of [1, 65535] range", row.Port)}
		}
		// codeql[go/incorrect-integer-conversion]
		port32 = int32(row.Port)
		_, err = s.store.InsertDataUpstream(ctx, sqlc.InsertDataUpstreamParams{
			ID:        state.NewPgtypeUUID(uuid.New()),
			AccountID: state.NewPgtypeUUID(acctUUID),
			AppID:     state.NewPgtypeUUID(appUUID),
			Source:    string(api.DataUpstreamSourceInferred),
			Scope:     scope,
			// DeploymentScope widens the dedupe key in ADR-098
			// amendment (issue #954). Resolved once per run via
			// s.deploymentScopeForEnvRow; on ErrNotFound falls
			// back to defaultEnvScope (the migration's SQL
			// DEFAULT matches this fallback).
			DeploymentScope:  deploymentScope,
			Kind:             row.Kind,
			Host:             row.Host,
			Port:             port32,
			HostRedactedHash: hash,
			DeclaredRegion:   state.Text{},
			LastRttMs:        state.Int4{},
			LastProbedAt:     state.Timestamptz{},
		})
		if err != nil {
			return &errEnvClassifier{Kind: errClassifierInsert.Kind, Err: err}
		}
		s.log.Info("data_upstream inferred",
			"app", app.Slug,
			"account", acct.ID,
			"kind", row.Kind,
			"scope", logsanitize.Field(scope),
			"deployment_scope", logsanitize.Field(deploymentScope),
			"env_key", logsanitize.Field(key),
			"host_redacted_hash", hash[:8],
		)
		s.audit.Emit(ctx, "data_upstream.inferred", &acct.ID, map[string]any{
			"app_id":             app.ID,
			"kind":               row.Kind,
			"scope":              scope,
			"deployment_scope":   deploymentScope,
			"env_key":            key,
			"host_redacted_hash": hash[:8],
		})
	}
	return nil
}

// deploymentScopeForEnvRow resolves the deployment-scope stamp for
// an env-classifier row (ADR-098 amendment, issue #954). The
// classifier writes data_upstreams rows with a per-deployment
// dedupe key, and the deployment the env-scope targets is the
// deployment the captured upstream belongs to.
//
// Uses the existing pgstore.LiveDeploymentForScope shim
// (pkg/state/pgstore.go:4111, backed by partial UNIQUE index
// deployments_app_scope_live_uniq from migration 00213).
//
// Contract:
//   - Empty envScope → (defaultEnvScope, nil): no resolver needed;
//     migration DEFAULT handles this case identically.
//   - ErrNotFound → (defaultEnvScope, nil): no live deployment
//     targets the env-scope (e.g. customer has not yet created
//     a deployment for that scope). This is the normal
//     single-deployment case and the writer-time contract on
//     docs/adr/098-deployment-scope-overlay.md §D3 demands
//     non-error fallback.
//   - LiveDeploymentForScope returns a row with empty scope
//     → (defaultEnvScope, nil): same shape as ErrNotFound.
//   - Any other error → ("", err): caller logs + degrades.
//
// The caller (runEnvClassifier) is best-effort and fails open
// on the wrapped err: it logs + falls back to defaultEnvScope
// itself. The split here is what lets that caller treat the
// two cases symmetrically without a separate log line.
func (s *server) deploymentScopeForEnvRow(ctx context.Context, appUUID uuid.UUID, envScope string) (string, error) {
	if envScope == "" {
		// No env-scope → no deployment resolver needed; the
		// migration DEFAULT handles this case identically.
		return defaultEnvScope, nil
	}
	dep, err := s.store.LiveDeploymentForScope(ctx, appUUID.String(), envScope)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Expected path: no live deployment targets the
			// env-scope yet. Stamp the migration's DEFAULT
			// rather than failing the env mutation.
			return defaultEnvScope, nil
		}
		// Real DB error (timeout, conn-reset, etc.) — propagate
		// so the caller can log + degrade.
		return "", err
	}
	if dep.Scope == "" {
		return defaultEnvScope, nil
	}
	return dep.Scope, nil
}
