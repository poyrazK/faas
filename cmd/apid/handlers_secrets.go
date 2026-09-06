// handlers_secrets.go — apid handlers for customer secrets (spec §11/G2).
//
// Routes (registered in server.go::handler):
//
//	GET    /v1/apps/{slug}/secrets              → listSecrets
//	PUT    /v1/apps/{slug}/secrets/{key}        → setSecret
//	DELETE /v1/apps/{slug}/secrets/{key}        → deleteSecret
//
// Trust model
//
//   - Plaintext VALUE arrives over TLS via PUT body, lives transiently in
//     this handler, and is sealed by pkg/secretbox.SealOne against the
//     host age recipient before it lands in PG. The ciphertext flows back
//     out of apid only via schedd → vmmd at wake time.
//   - s.recipient is the *age.X25519Recipient loaded at startup from
//     /etc/faas/secrets/host.age.pub. apid refuses to start if the file
//     is missing — a misconfigured box must NOT silently accept plaintext
//     it has nowhere to seal to.
//   - No log line ever contains the plaintext VALUE. Key names are public
//     per spec §11 and flow freely.

package main

import (
	"context"
	"errors"
	"filippo.io/age"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// stdctx is the alias we use internally for handlers that take ctx directly
// (avoids the `ctx` shadowing the local variable pattern in some handlers).
type stdctx = context.Context

// setSecretRecipient is the host X25519 recipient apid loads once at
// startup. Held as a *age.X25519Recipient because SealOne doesn't need the
// private half — only vmmd holds that (pkg/fcvm loads host.age at boot).
//
// Setting this is the responsibility of cmd/apid/main.go's run path. The
// nil-default makes tests that don't seal pass without plumbing; a
// production apid that forgets to load the recipient will surface a clear
// 503 from every PUT (no silent accept-and-drop).
var setSecretRecipient func() *age.X25519Recipient

// hostHMACKey is the per-host HMAC key that secretbox.ValueFingerprint
// uses to compute the trustworthy value-equality discriminator on the
// sealed envelope (ADR-117 env-diff matrix, PR-C). Loaded once at
// apid startup from /etc/faas/secrets/host.hmac.key (32 bytes,
// mode 0o400 OR 0o440) and held as a func() []byte seam that
// mirrors setSecretRecipient.
//
// Write-time only: sealAndPersist (PUT + rotate paths) and the
// rekey worker (pkg/rekey/rekey.go) consult hostHMACKey at
// write time. The diff endpoint reads value_hash off the row
// directly and never needs the HMAC key. A missing key causes
// the apid to refuse to start (cmd/apid/main.go) — see
// ADR-117 D2 + D9.
//
// Setting this is the responsibility of cmd/apid/main.go's run path.
// Tests that don't seal pass without plumbing.
var hostHMACKey func() []byte

// listSecrets returns every secret on the app, key + timestamps only.
// Ciphertext never leaves apid except via schedd → vmmd. Quota info is
// included so the CLI can show "3/25 secrets" without a separate call.
//
// `?scope=__all__` returns a nested `secrets_by_scope` map shape
// (ADR-092, mirror of ADR-090 D3's env_by_scope); the flat `secrets`
// array is empty. All other scopes return the flat `secrets` array as
// before. The count + quota fields count across ALL scopes (the
// per-app SecretCountMax is unchanged across scopes per ADR-090 D6 —
// see the pkg/api/limits.go comment block).
func (s *server) listSecrets(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
		// row count is capped at Limits.SecretCountMax.
		rows, err := s.store.ListAllAppSecrets(r.Context(), acct.ID, app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
			return
		}
		writeSecretListAll(w, rows, limits.SecretCountMax)
		return
	}
	s.listSecretsInScope(w, r, acct, app, scope, limits)
}

// listSecretsInScope renders the per-scope flat arm of the GET
// response. Extracted from listSecrets (handlers_secrets.go:62) so
// the routing handler stays under the 50-line cap (CLAUDE.md).
// The cross-scope Count is queried explicitly so the CLI stamp and
// dashboard render a single "N / SecretCountMax" bar regardless of
// which scope the customer is currently inspecting (ADR-090 D6 /
// ADR-092 D6 posture).
func (s *server) listSecretsInScope(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, scope string, limits api.Limits) {
	rows, err := s.store.ListAppSecretsInScope(r.Context(), acct.ID, app.ID, scope)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
		return
	}
	out := make([]api.AppSecretResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.AppSecretResponse{
			Key:       row.Key,
			Scope:     row.Scope,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
			Kid:       row.Kid,
			ValueHash: row.ValueHash,
		})
	}
	totalCount, err := s.store.CountAppSecrets(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count secrets"))
		return
	}
	writeJSON(w, http.StatusOK, api.AppSecretListResponse{
		Secrets: out,
		Quota:   limits.SecretCountMax,
		Count:   totalCount,
	})
}

// writeSecretListAll renders the nested `secrets_by_scope` response
// shape for `?scope=__all__`. The flat `secrets` array is empty; the
// map keys are scope names. Rows are grouped by scope and the
// per-scope slice is sorted by key ASC to match the flat response.
// Count is the row total (cross-scope); Quota is unchanged.
//
// Mirror of writeEnvListAll (handlers_env.go:209) — the env route
// already uses this discriminated-union shape (ADR-090 PR-B), and
// secrets deliberately re-use the same rendering rule for symmetry.
func writeSecretListAll(w http.ResponseWriter, rows []state.AppSecret, quota int) {
	bucket := map[string][]api.ScopedAppSecretResponse{}
	for _, r := range rows {
		bucket[r.Scope] = append(bucket[r.Scope], api.ScopedAppSecretResponse{
			Scope:     r.Scope,
			Key:       r.Key,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
			Kid:       r.Kid,
			ValueHash: r.ValueHash,
		})
	}
	for scope := range bucket {
		sort.Slice(bucket[scope], func(i, j int) bool {
			return bucket[scope][i].Key < bucket[scope][j].Key
		})
	}
	scopes := make([]string, 0, len(bucket))
	for scope := range bucket {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	ordered := make(api.SecretByScope, len(bucket))
	for _, scope := range scopes {
		ordered[scope] = bucket[scope]
	}
	writeJSON(w, http.StatusOK, api.AppSecretListResponse{
		Secrets:        nil, // discriminated union: empty in the __all__ arm
		SecretsByScope: ordered,
		Quota:          quota,
		Count:          len(rows),
	})
}

// setSecret seals the plaintext VALUE and upserts the (app_id, key) row.
// Quota is enforced before the seal so an over-cap request is rejected
// before any seal work happens. Idempotent: re-PUT replaces ciphertext +
// bumps updated_at.
//
// Hand-rolled phases, not a helper, because the line budget here is well
// under the §Conventions 50-line cap and the phase order matters for
// auditing (validate key → resolve app → validate body → check quota →
// seal → persist → log).
func (s *server) setSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	key := r.PathValue("key")
	if prob := api.ValidateSecretKey(key); prob != nil {
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
	var req api.PutAppSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	// Byte cap: enforced here AND inside secretbox.SealOne. Defense in
	// depth — if a future refactor drops one of the two, the other still
	// protects.
	if prob := req.Validate(limits.SecretValueMaxBytes); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.checkSecretQuota(r.Context(), acct, app, scope, key, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.sealAndPersist(r.Context(), acct, app, scope, key, req.Value, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Audit + log. VALUE never reaches slog. logsanitize.RedactValue is
	// used defensively even though we never log req.Value directly — a
	// future refactor that adds a "request echo" log line won't leak.
	s.log.Info("secret set",
		"app", app.Slug,
		"key", logsanitize.Field(key),
		"scope", scope,
		"account", acct.ID,
		"value_bytes", logsanitize.RedactValue(req.Value),
	)
	// IAM-4 (ADR-035): record the secret set. NEVER carry the
	// plaintext value in the audit row — same posture as slog.
	// data.app_id lets the audit log filter by app; data.name is
	// the secret key (not the value); data.scope is the env-scope
	// the row was written to (ADR-092 PR-B).
	s.audit.Emit(r.Context(), "secret.set", &acct.ID, map[string]any{
		"app_id": app.ID,
		"name":   key,
		"scope":  scope,
	})
	writeJSON(w, http.StatusOK, struct {
		Key string `json:"key"`
	}{Key: key})
}

// sealAndPersist runs the "no-recipient / seal / persist" portion of
// setSecret. Pulled out so the handler itself reads as a sequence of
// guards, each calling a single check. Returns nil on success or a
// ready-to-write *api.Problem on failure.
//
// ADR-089 PR-A added app_secrets.kid; PR-C discovered that the PUT
// path was leaving kid = NULL because it called UpsertAppSecret
// (no-kid) instead of UpsertAppSecretWithKid. The rotate handler
// reads kid back via GetAppSecret, and pgx v5 cannot scan NULL
// into a Go string — so a row written by the PUT path 500s the
// rotate handler. This helper now stamps kid alongside ciphertext
// so the PUT path is self-consistent with the rotate path and
// pkg/rekey.Replayer can reseal rows without first unsealing NULL.
//
// ADR-117 PR-C widens the row with value_hash (HMAC-SHA256 over
// the PLAINTEXT, keyed by hostHMACKey, truncated to 16 hex). The
// hash is computed BEFORE SealOne because age X25519 +
// ChaCha20-Poly1305 is probabilistically non-deterministic — two
// calls over the same plaintext produce byte-different ciphertexts,
// so a ciphertext-derived hash would diverge for every row and the
// env-diff discriminator would be useless. The plaintext variant
// is the only one that produces the "same hash across scopes =
// same plaintext" property the env-diff endpoint relies on.
//
// The kid is computed from mfaIdentities()[0] (the current host
// identity). If identities are not loaded we refuse to seal (same
// posture as the rotate handler — a typo'd env var shouldn't
// silently degrade to "no kid, no problem"). Same refuses-to-start
// posture for the host HMAC key — apid boots without one ONLY in
// the env-disabled path; calling sealAndPersist without a host
// HMAC key is a misconfiguration that must surface as a 5xx, not
// a silently empty value_hash.
func (s *server) sealAndPersist(c stdctx, acct state.Account, app state.App, scope, key, value string, limits api.Limits) *api.Problem {
	recipient := setSecretRecipient()
	if recipient == nil {
		// Apid started without a host.age.pub; refuse to accept plaintext.
		return api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	hmacKey := hostHMACKey()
	if len(hmacKey) == 0 {
		// ADR-117 PR-C: defensible refuse-to-seal for the case where
		// env-driven secrets are enabled but the per-host HMAC key
		// didn't load. apid startup catches this earlier (503 at boot),
		// but a unit test that bypasses main's loader must not be
		// able to silently write a row with value_hash = ''.
		return api.ErrCapacity("host hmac key not loaded — refusing to seal")
	}
	// mfaIdentities is nil in unit-test harnesses that only install
	// the single-key package level (see withTestRecipient in
	// handlers_secrets_test.go); the same nil-guard pattern lives in
	// handlers_mfa.go:600-602. We fall back to mfaIdentity so tests
	// that haven't migrated to the rotation-aware accessor stamp a
	// kid without a separate setup helper.
	var idents []*age.X25519Identity
	if mfaIdentities != nil {
		idents = mfaIdentities()
	}
	if len(idents) == 0 && mfaIdentity != nil {
		if single := mfaIdentity(); single != nil {
			idents = []*age.X25519Identity{single}
		}
	}
	if len(idents) == 0 {
		return api.ErrCapacity("host age identities not loaded — refusing to seal")
	}
	kid, err := secretbox.IdentityFingerprint(idents)
	if err != nil {
		return api.ErrCapacity("could not resolve kid: " + err.Error())
	}
	valueHash, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
	if err != nil {
		// Empty plaintext is a 400 from the handler chain (limits /
		// SecretValueMaxBytes >= 1) — ValueFingerprint's empty-input
		// error only fires for the empty-string edge case the
		// handler didn't catch. Treat as a 5xx capacity problem
		// (misconfiguration: handler let an empty value through).
		return api.ErrCapacity("could not compute value_hash: " + err.Error())
	}
	ciphertext, err := secretbox.SealOne(recipient, key, value, limits.SecretValueMaxBytes)
	if err != nil {
		// SealOne may return an api.Problem (over-cap) — surface it directly.
		if prob := api.AsProblem(err); prob != nil {
			return prob
		}
		return api.ErrCapacity("could not seal secret")
	}
	// ADR-092 PR-B: scope-aware upsert. PK is now (app_id, scope, key)
	// (PR-A migration 00217). The flat UpsertAppSecretWithKid is kept
	// as a sibling interface for legacy callers; new callers MUST
	// thread the scope from `?scope=` through scopeFromQuery →
	// sealAndPersist.
	//
	// ADR-117 PR-C: value_hash is the value-hash discriminator the
	// env-diff endpoint reads. Stamped alongside ciphertext so the
	// row is usable by the diff surface immediately.
	if err := s.store.UpsertAppSecretWithKidAndValueHashInScope(c, acct.ID, app.ID, scope, key, kid, valueHash, ciphertext); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return api.ErrManagedSecretConflict()
		}
		return api.ErrCapacity("could not persist secret")
	}
	return nil
}

// checkSecretQuota returns nil when a PUT for (app, scope, key) is
// allowed under the per-plan SecretCountMax, or a ready-to-write
// *api.Problem otherwise. Re-PUTs of an existing (scope, key) are
// not new rows and so don't count against the quota — the
// (count - 1) for the row being replaced is implicit.
//
// Per ADR-090 D6 (and the parallel ADR-092 posture in
// pkg/api/limits.go::SecretCountMax), the quota counts across every
// scope the customer has minted — a customer's "staging" rows
// count toward the same SecretCountMax as their "default" rows.
// secretExistsInScope's per-scope check subtracts 1 only when the
// (scope, key) being PUT already exists.
//
// A nil *api.Problem means "proceed"; a non-nil one means "refuse, with
// this problem envelope". This shape keeps setSecret itself readable: it
// reads as a sequence of guards, each calling a single check.
func (s *server) checkSecretQuota(c stdctx, acct state.Account, app state.App, scope, key string, limits api.Limits) *api.Problem {
	n, err := s.store.CountAppSecrets(c, acct.ID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not count secrets")
	}
	already, err := s.secretExistsInScope(c, acct.ID, app.ID, scope, key)
	if err != nil {
		return api.ErrCapacity("could not check secret")
	}
	if !already && n >= limits.SecretCountMax {
		return api.ErrPlanLimitSecrets(limits, n)
	}
	return nil
}

// deleteSecret removes the (app_id, scope, key) row. 400
// CodeSecretNotFound when the (scope, key) pair isn't set — distinct
// from 404 because the URL resource IS the secret name.
//
// `?scope=` selects which scope to delete from; the reserved sentinel
// `__all__` is rejected (400 env_scope_reserved) because it has no
// meaning on a single-row write. Omitted scope means
// `scope=default` — pre-PR-B callers see no behaviour change.
func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	key := r.PathValue("key")
	if prob := api.ValidateSecretKey(key); prob != nil {
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
	if err := s.store.DeleteAppSecretInScope(r.Context(), acct.ID, app.ID, scope, key); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrSecretNotFound(key))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrManagedSecretConflict())
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete secret"))
		return
	}
	s.log.Info("secret deleted",
		"app", app.Slug,
		"key", logsanitize.Field(key),
		"scope", scope,
		"account", acct.ID,
	)
	// IAM-4 (ADR-035): record the secret delete. data.scope is the
	// env-scope the row was deleted from (ADR-092 PR-B).
	s.audit.Emit(r.Context(), "secret.deleted", &acct.ID, map[string]any{
		"app_id": app.ID,
		"name":   key,
		"scope":  scope,
	})
	w.WriteHeader(http.StatusNoContent)
}

// secretExistsInScope checks if a (app_id, scope, key) row exists for
// the account. Used by setSecret to subtract 1 from the quota count
// when an upsert is replacing an existing row.
//
// Mirror of envExistsInScope (handlers_env.go:458). The check is
// O(secret count) per scope, not O(secrets total) — bounded by
// Limits.SecretCountMax (≤ 100 across plans), so even a linear
// scan is trivially fast. We don't add a dedicated Store method to
// avoid a fifth interface surface; ListAppSecretsInScope already
// returns the keys for the scope.
func (s *server) secretExistsInScope(c stdctx, accountID, appID, scope, key string) (bool, error) {
	rows, err := s.store.ListAppSecretsInScope(c, accountID, appID, scope)
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
