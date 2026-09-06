// handlers_secrets_rotate.go — POST /v1/apps/{slug}/secrets/{key}/rotate
// (ADR-089 PR-B).
//
// Rotates one app secret in place: re-seals the row under the current
// host identity, stamps kid = current, and emits the secret.rotated
// audit kind (vs. secret.set on the PUT path). The endpoint is
// distinct from PUT for two reasons:
//
//  1. Audit taxonomy. Dashboards filtering on kind='secret.rotated'
//     want "value changed" semantics, not "row exists" semantics.
//     Rotating an existing value emits secret.rotated; setting a
//     fresh key emits secret.set. The audit_log.actor column
//     (migrations/00163) further separates user-initiated
//     (actor='apid') from background-driven re-seals
//     (actor='rekey', wired in PR-C).
//
//  2. Trust boundary. Rotation writes a NEW credential value. Same
//     scope + MFA posture as secrets:write — losing the new value
//     is the loss-bearing case (an attacker reading the old value
//     is unaffected; an attacker reading the new value gains
//     current DB access). The MFA gate is the same one
//     ScopesSecretsWriteSurface enforces; we don't add a new
//     rotation-specific scope.
//
// Out of scope: re-seal of webhook_secret_sealed or
// alert_rule_secret_sealed. Those surfaces have their own
// rotate-secret verbs (issue #676 / ADR-080 for webhooks, issue
// #476 for alert rules). The pkg/rekey package handles app_secrets
// only.

package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// rotateAppSecret seals the plaintext VALUE under the current host
// identity and overwrites the (app_id, key) row. Emits secret.rotated
// when the row already had a value (the dominant case — rotating an
// existing credential) and secret.set when the row was previously
// empty.
//
// Hand-rolled phases, not a helper, because the line budget is well
// under the §Conventions 50-line cap and the phase order matters for
// auditing (validate key → resolve app → validate body → check
// existence → seal → persist → log → audit).
//
// The kid fingerprint is read via mfaIdentities — the same accessor
// handlers_mfa.go uses for OpenMulti. That accessor returns the
// multi-identity slice loaded by secretbox.LoadHostKeys(dir) at
// startup (current first, previous second); kid stamping reads
// identities[0] which is the "current" recipient by the hostkey.go
// ordering convention.
func (s *server) rotateAppSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	var req api.RotateAppSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if prob := req.Validate(limits.SecretValueMaxBytes); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	// Read the previous row so we can decide audit kind: secret.set
	// (no prior value) vs secret.rotated (rotation of an existing
	// row). GetAppSecretInScope returns ErrNotFound when the row
	// does not exist; that's the "first-time rotate" case which we
	// treat as a set for audit purposes.
	//
	// CONTRACT pinned at the handler boundary: GetAppSecretInScope's
	// three-state result must be either (row, nil), (nil,
	// ErrNotFound), or (nil, err) — never (nil, nil). If a future
	// refactor relaxes GetAppSecretInScope to (nil, nil) on an empty
	// result, we want the handler to 5xx loudly rather than
	// silently emit secret.set for a row that may already have a
	// value (which would understate rotations in the audit log).
	prev, err := s.store.GetAppSecretInScope(r.Context(), acct.ID, app.ID, scope, key)
	switch {
	case err == nil && prev == nil:
		api.WriteProblem(w, api.ErrCapacity("GetAppSecretInScope returned (nil, nil) — store contract broken"))
		return
	case err != nil && !errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrCapacity("could not read previous secret"))
		return
	}
	isRotation := err == nil

	// Resolve the current kid before sealing so the seal + kid
	// stamp land in the same UpsertAppSecretWithKid call. Failure
	// to fingerprint (no identities loaded) returns 503 — refusing
	// to seal without a kid is consistent with refusing to seal
	// without a recipient.
	idents := mfaIdentities()
	if len(idents) == 0 {
		api.WriteProblem(w, api.ErrCapacity("host age identities not loaded — refusing to seal"))
		return
	}
	kid, err := secretbox.IdentityFingerprint(idents)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve kid: "+err.Error()))
		return
	}

	if prob := s.sealAndPersistWithKid(r.Context(), acct, app, scope, key, req.Value, limits, kid); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	now := time.Now().UTC()
	s.log.Info("secret rotated",
		"app", app.Slug,
		"key", logsanitize.Field(key),
		"scope", scope,
		"account", acct.ID,
		"value_bytes", logsanitize.RedactValue(req.Value),
		"kid", kid,
	)

	// ADR-089 D2: distinct audit kinds. secret.set for first-time
	// writes (handled by setSecret); secret.rotated for
	// value-replacement events. The audit_log.actor column
	// (migration 00163) further distinguishes user-driven rotate
	// (this handler, actor='apid') from background re-seal
	// (pkg/rekey.Replayer, actor='rekey' — PR-C). ADR-092 PR-B
	// adds data.scope so dashboards can group rotations by scope.
	auditKind := "secret.rotated"
	if !isRotation {
		auditKind = "secret.set"
	}
	// RFC3339Nano so two rotates in the same second produce
	// distinct timestamps. RFC3339 truncates to the second, and a
	// test that hits the same wall-clock second would observe
	// identical rotated_at strings — false alarm. RFC3339Nano
	// matches the spec's millisecond audit-grain convention.
	nowStr := now.Format(time.RFC3339Nano)
	s.audit.Emit(r.Context(), auditKind, &acct.ID, map[string]any{
		"app_id":     app.ID,
		"name":       key,
		"scope":      scope,
		"kid":        kid,
		"rotated_at": nowStr,
	})

	writeJSON(w, http.StatusOK, api.RotateAppSecretResponse{
		Key:       key,
		RotatedAt: nowStr,
		Kid:       kid,
	})
}

// sealAndPersistWithKid mirrors sealAndPersist (handlers_secrets.go)
// but stamps the kid column. Pulled out so the handler itself reads
// as a sequence of guards, each calling a single check. Returns nil
// on success or a ready-to-write *api.Problem on failure.
//
// Recipient lookup reuses setSecretRecipient (the same package-level
// accessor setSecret uses). The kid fingerprint is computed in the
// caller and passed in — the seal path doesn't need to know how to
// fingerprint, only that the kid is correct.
//
// ADR-092 PR-B: the scope arg threads through to
// UpsertAppSecretWithKidInScope — PK is (app_id, scope, key).
//
// ADR-117 PR-C: value_hash is computed BEFORE SealOne (plaintext
// form, NOT ciphertext) for the same reason as in sealAndPersist:
// age is probabilistically non-deterministic so a ciphertext-derived
// hash is useless as the env-diff discriminator. The kid is
// caller-supplied and identity-shaped; the value_hash is computed
// locally off the plaintext.
func (s *server) sealAndPersistWithKid(c stdctx, acct state.Account, app state.App, scope, key, value string, limits api.Limits, kid string) *api.Problem {
	recipient := setSecretRecipient()
	if recipient == nil {
		return api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	hmacKey := hostHMACKey()
	if len(hmacKey) == 0 {
		return api.ErrCapacity("host hmac key not loaded — refusing to seal")
	}
	valueHash, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
	if err != nil {
		return api.ErrCapacity("could not compute value_hash: " + err.Error())
	}
	ciphertext, err := secretbox.SealOne(recipient, key, value, limits.SecretValueMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return prob
		}
		return api.ErrCapacity("could not seal secret")
	}
	if err := s.store.UpsertAppSecretWithKidAndValueHashInScope(c, acct.ID, app.ID, scope, key, kid, valueHash, ciphertext); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return api.ErrManagedSecretConflict()
		}
		return api.ErrCapacity("could not persist secret")
	}
	return nil
}
