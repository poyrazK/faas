// handlers_env_diff.go — GET /v1/apps/{slug}/env-diff
// (ADR-117 PR-C).
//
// The env-diff matrix reads every secret + env var on the app,
// unions the keys, and renders a (rows × scopes) matrix where
// each cell carries {present, value_hash (secret) | value (env)}.
//
// Design notes (locked with the user 2026-08-19):
//
//   - Single endpoint, no `?scope=` filter in v1 — the matrix
//     IS the whole point. A per-scope filter would be a strange
//     query. Deferred to a follow-up if a dashboard needs it.
//   - Secrets never reveal plaintext. EnvDiffCell has no
//     `value` field for a secret row, and no `value_hash` for
//     an env row. This is the load-bearing security property
//     of the endpoint (ADR-117 D5).
//   - Pre-PR-C rows (value_hash = '') emit no `value_hash` key
//     in the cell. The dashboard treats "absent value_hash" as
//     "unknown" (cell renders as "-" or "missing").
//   - The matrix is bounded: rows <= SecretCountMax +
//     EnvCountMax, columns = the customer's scope set (1-3
//     typical). A 4-scope × 200-row matrix is well within
//     JSON parsing budget.

package main

import (
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// envDiff handles GET /v1/apps/{slug}/env-diff. The route is
// registered in server.go alongside the other read surfaces
// (listEnv, listSecrets). Authorization matches listEnv — read
// surface scope, NOT MFA-gated, since the diff endpoint
// surfaces no secret plaintext (only equality / presence
// signals).
func (s *server) envDiff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	secrets, err := s.store.ListAllAppSecrets(r.Context(), acct.ID, app.ID)
	if err != nil {
		// ListAllAppSecrets shares the same error surface as
		// listSecrets (handlers_secrets.go) — a 5xx capacity
		// is the right shape. The dashboard retries; the
		// customer-facing CLI surfaces "could not list
		// secrets" with a retry hint.
		api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
		return
	}
	envs, err := s.store.ListAllAppEnv(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list env vars"))
		return
	}
	resp := buildEnvDiffResponse(app.Slug, secrets, envs)
	writeJSON(w, http.StatusOK, resp)
}

// buildEnvDiffResponse is the pure-Go matrix builder. Lives in
// the handler file (NOT in pkg/state — pure helpers don't
// belong at the persistence boundary per ADR-117 §Issue 8).
//
// Algorithm:
//
//  1. Collect every key from (secrets + envs).
//  2. Collect the unioned scope set.
//  3. Sort keys ASC, sort scopes ASC.
//  4. For each key, build an EnvDiffRow with a Cells map keyed
//     by scope. Every (key, scope) pair has a cell; cells for
//     missing (key, scope) pairs carry Present: false.
//  5. Stamp GeneratedAt with time.Now().UTC().
//
// ADR-117 D3: secret cells carry {present, value_hash}; env
// cells carry {present, value}. A row sourced from BOTH (a
// key named "DATABASE_URL" in both app_secrets and app_envs)
// renders as TWO rows — the matrix is unioned by KEY but each
// row's Kind discriminates the source. This is intentional:
// the customer wants to see "is DATABASE_URL the secret OR
// the env var? is one missing where the other is set?" and
// the unioned-by-key-but-not-by-kind shape is the answer.
func buildEnvDiffResponse(slug string, secrets []state.AppSecret, envs []state.AppEnv) api.EnvDiffResponse {
	// 1. Collect the scope union.
	scopeSet := map[string]struct{}{}
	for _, sec := range secrets {
		if sec.Scope != "" {
			scopeSet[sec.Scope] = struct{}{}
		}
	}
	for _, env := range envs {
		if env.Scope != "" {
			scopeSet[env.Scope] = struct{}{}
		}
	}
	scopes := make([]string, 0, len(scopeSet))
	for sc := range scopeSet {
		scopes = append(scopes, sc)
	}
	sort.Strings(scopes)

	// 2. Index secrets + envs by (key, scope).
	type scopeKey struct {
		key   string
		scope string
	}
	secretByKS := map[scopeKey]state.AppSecret{}
	for _, sec := range secrets {
		secretByKS[scopeKey{sec.Key, sec.Scope}] = sec
	}
	envByKS := map[scopeKey]state.AppEnv{}
	for _, env := range envs {
		envByKS[scopeKey{env.Key, env.Scope}] = env
	}

	// 3. Build the keyed-by-key set. Two rows can share a key
	// if the key is in both app_secrets and app_envs — the
	// Kind field disambiguates. Build a set of (key, kind)
	// pairs so the row count is unioned but never duplicated.
	type rowKey struct {
		key  string
		kind api.EnvDiffKind
	}
	rowSet := map[rowKey]struct{}{}
	for _, sec := range secrets {
		rowSet[rowKey{sec.Key, api.EnvDiffKindSecret}] = struct{}{}
	}
	for _, env := range envs {
		rowSet[rowKey{env.Key, api.EnvDiffKindEnv}] = struct{}{}
	}
	rowKeys := make([]rowKey, 0, len(rowSet))
	for rk := range rowSet {
		rowKeys = append(rowKeys, rk)
	}
	// Sort by key ASC; ties broken by kind (env < secret) for
	// stable wire shape (secret rows after env rows when both
	// exist for the same key).
	sort.Slice(rowKeys, func(i, j int) bool {
		if rowKeys[i].key != rowKeys[j].key {
			return rowKeys[i].key < rowKeys[j].key
		}
		return rowKeys[i].kind < rowKeys[j].kind
	})

	// 4. Build the rows. The matrix is unioned: every
	// (key, scope) pair has a cell, even when the row is
	// missing in this scope. Pre-populate every cell with
	// Present: false; overwrite with Present: true and the
	// value_hash / value when the row exists.
	rows := make([]api.EnvDiffRow, 0, len(rowKeys))
	for _, rk := range rowKeys {
		cells := make(map[string]api.EnvDiffCell, len(scopes))
		for _, sc := range scopes {
			cells[sc] = api.EnvDiffCell{Present: false}
		}
		for _, sc := range scopes {
			sk := scopeKey{rk.key, sc}
			switch rk.kind {
			case api.EnvDiffKindSecret:
				if sec, ok := secretByKS[sk]; ok {
					cells[sc] = api.EnvDiffCell{
						Present:   true,
						ValueHash: sec.ValueHash, // omitempty: pre-PR-C "" emits no field
					}
				}
			case api.EnvDiffKindEnv:
				if env, ok := envByKS[sk]; ok {
					cells[sc] = api.EnvDiffCell{
						Present: true,
						Value:   env.Value, // omitempty: missing (Present=false) emits no field
					}
				}
			}
		}
		rows = append(rows, api.EnvDiffRow{
			Key:   rk.key,
			Kind:  rk.kind,
			Cells: cells,
		})
	}

	return api.EnvDiffResponse{
		AppSlug:     slug,
		Scopes:      scopes,
		Rows:        rows,
		GeneratedAt: time.Now().UTC(),
	}
}
