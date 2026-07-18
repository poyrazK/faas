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

// listSecrets returns every secret on the app, key + timestamps only.
// Ciphertext never leaves apid except via schedd → vmmd. Quota info is
// included so the CLI can show "3/25 secrets" without a separate call.
func (s *server) listSecrets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	rows, err := s.store.ListAppSecrets(ctx(r), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list secrets"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	out := make([]api.AppSecretResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.AppSecretResponse{
			Key:       row.Key,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	// Wrap with quota metadata so the CLI can render a progress bar.
	writeJSON(w, http.StatusOK, struct {
		Secrets []api.AppSecretResponse `json:"secrets"`
		Quota   int                     `json:"quota_max"`
		Count   int                     `json:"count"`
	}{Secrets: out, Quota: limits.SecretCountMax, Count: len(out)})
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
	if prob := s.checkSecretQuota(ctx(r), acct, app, key, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.sealAndPersist(ctx(r), acct, app, key, req.Value, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Audit + log. VALUE never reaches slog. logsanitize.RedactValue is
	// used defensively even though we never log req.Value directly — a
	// future refactor that adds a "request echo" log line won't leak.
	s.log.Info("secret set",
		"app", app.Slug,
		"key", logsanitize.Field(key),
		"account", acct.ID,
		"value_bytes", logsanitize.RedactValue(req.Value),
	)
	writeJSON(w, http.StatusOK, struct {
		Key string `json:"key"`
	}{Key: key})
}

// sealAndPersist runs the "no-recipient / seal / persist" portion of
// setSecret. Pulled out so the handler itself reads as a sequence of
// guards, each calling a single check. Returns nil on success or a
// ready-to-write *api.Problem on failure.
func (s *server) sealAndPersist(c stdctx, acct state.Account, app state.App, key, value string, limits api.Limits) *api.Problem {
	recipient := setSecretRecipient()
	if recipient == nil {
		// Apid started without a host.age.pub; refuse to accept plaintext.
		return api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	ciphertext, err := secretbox.SealOne(recipient, key, value, limits.SecretValueMaxBytes)
	if err != nil {
		// SealOne may return an api.Problem (over-cap) — surface it directly.
		if prob := api.AsProblem(err); prob != nil {
			return prob
		}
		return api.ErrCapacity("could not seal secret")
	}
	if err := s.store.UpsertAppSecret(c, acct.ID, app.ID, key, ciphertext); err != nil {
		return api.ErrCapacity("could not persist secret")
	}
	return nil
}

// checkSecretQuota returns nil when a PUT for (app, key) is allowed under
// the per-plan SecretCountMax, or a ready-to-write *api.Problem otherwise.
// Re-PUTs of an existing key are not new rows and so don't count against
// the quota — the (count - 1) for the row being replaced is implicit.
//
// A nil *api.Problem means "proceed"; a non-nil one means "refuse, with
// this problem envelope". This shape keeps setSecret itself readable: it
// reads as a sequence of guards, each calling a single check.
func (s *server) checkSecretQuota(c stdctx, acct state.Account, app state.App, key string, limits api.Limits) *api.Problem {
	n, err := s.store.CountAppSecrets(c, acct.ID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not count secrets")
	}
	already, err := s.secretExists(c, acct.ID, app.ID, key)
	if err != nil {
		return api.ErrCapacity("could not check secret")
	}
	if !already && n >= limits.SecretCountMax {
		return api.ErrPlanLimitSecrets(limits, n)
	}
	return nil
}

// deleteSecret removes the (app_id, key) row. 400 CodeSecretNotFound when
// the key isn't set — distinct from 404 because the URL resource IS the
// secret name.
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
	if err := s.store.DeleteAppSecret(ctx(r), acct.ID, app.ID, key); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrSecretNotFound(key))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete secret"))
		return
	}
	s.log.Info("secret deleted",
		"app", app.Slug,
		"key", logsanitize.Field(key),
		"account", acct.ID,
	)
	w.WriteHeader(http.StatusNoContent)
}

// secretExists checks if a (app_id, key) row exists for the account.
// Used by setSecret to subtract 1 from the quota count when an upsert is
// replacing an existing row.
//
// The check is O(secret count) per app, not O(secrets total) — bounded by
// Limits.SecretCountMax (≤ 100 across plans), so even a linear scan is
// trivially fast. We don't add a dedicated Store method to avoid a fifth
// interface surface; ListAppSecrets already returns the keys.
func (s *server) secretExists(c stdctx, accountID, appID, key string) (bool, error) {
	rows, err := s.store.ListAppSecrets(c, accountID, appID)
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
