// handlers_registry_auth.go — apid handlers for per-app private-registry
// Basic Auth (issue #461 / ADR-062).
//
// Routes (registered in server.go::handler):
//
//	GET    /v1/apps/{slug}/registry-credentials                   → listRegistryCredentials
//	PUT    /v1/apps/{slug}/registry-credentials                   → setRegistryCredential
//	DELETE /v1/apps/{slug}/registry-credentials?registry=...      → deleteRegistryCredential
//
// Trust model (mirrors handlers_secrets.go)
//
//   - Plaintext PASSWORD arrives over TLS via PUT body, lives transiently
//     in this handler, and is sealed by pkg/secretbox.SealBytes under
//     namespace "registry_creds" before it lands in PG.
//   - setSecretRecipient is the SAME *age.X25519Recipient the secrets
//     handler uses — same key file (/etc/faas/secrets/host.age.pub),
//     same in-process lifetime. imaged unseals via the matching identity.
//   - No log line, audit payload, or returned error contains the
//     plaintext password. Username + registry host are public per the
//     secrets handler posture.
//   - DELETE uses ?registry=<host> query param (URL-encoded). Registry
//     hosts can carry a port — path segments don't escape cleanly.
package apidserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// MaxRegistryBodyBytes caps the PUT body. The wire shape carries
// {registry, username, password}; password can be up to
// MaxRegistryPasswordBytes (4 KiB) and username up to
// MaxRegistryUsernameLen (256 bytes); the body itself is well
// under a MiB even for the largest legitimate request. Mirror the
// secrets handler's 1 MiB decodeJSON cap.
const maxRegistryBodyBytes = 1 << 20

// normalizeRegistryHost lowercases the host, drops any leading
// "http://" / "https://" scheme, drops any trailing slash, and
// rejects any embedded path. The store stores the normalized
// form so apid, imaged, and the e2e harness agree on the lookup
// predicate. Validation against registryHostRe (RFC 1035 hostname
// + optional :port) lives in api.PutAppRegistryCredentialRequest.
// Validate; here we just normalize.
//
// Empty input is rejected at the boundary.
func normalizeRegistryHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("registry is required")
	}
	// HTTPS-only scheme. Plain http:// would put the customer's
	// Basic Auth on a cleartext wire (§11 tenant egress + §11
	// "never log secret values" implies we should also refuse to
	// SEND them in cleartext). Schemeless inputs are ambiguous
	// ("ghcr.io" could be a typo for a different host entirely)
	// and we refuse them at the boundary.
	switch {
	case strings.HasPrefix(strings.ToLower(s), "https://"):
		s = s[len("https://"):]
	case strings.HasPrefix(strings.ToLower(s), "http://"):
		return "", errors.New("registry must use https:// (cleartext is not allowed)")
	default:
		return "", errors.New("registry must include explicit https:// prefix")
	}
	s = strings.ToLower(s)
	// Drop trailing slashes (the typical user input).
	s = strings.TrimRight(s, "/")
	// Reject embedded paths / query / fragment — the host is the
	// only thing we want.
	if strings.ContainsAny(s, "/?#") {
		return "", errors.New("registry contains path/query/fragment")
	}
	return s, nil
}

// toAppRegistryCredentialResponse maps a state row to the wire
// response. NEVER copies the password — by design the password
// field doesn't exist on the response shape.
func toAppRegistryCredentialResponse(row state.AppRegistryCredential) api.AppRegistryCredentialResponse {
	resp := api.AppRegistryCredentialResponse{
		Registry:  row.Registry,
		Username:  row.Username,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.LastUsedAt != nil {
		resp.LastUsedAt = row.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// listRegistryCredentials returns every credential on the app:
// registry + username + timestamps only. Password NEVER leaves
// the store. Quota info is included so the CLI can render
// "2/5 hosts" without a separate call.
func (s *server) listRegistryCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	rows, err := s.store.ListAppRegistryCredentials(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list registry credentials"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	out := make([]api.AppRegistryCredentialResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAppRegistryCredentialResponse(row))
	}
	// Wrap with quota metadata so the CLI can render a progress bar.
	// AppRegistryCredentialListResponse.Credentials is `[]...` (not
	// nil) so the JSON is always `"credentials": []` for an empty
	// app — saves the CLI a nil check.
	writeJSON(w, http.StatusOK, api.AppRegistryCredentialListResponse{
		Credentials: out,
		QuotaMax:    limits.RegistryCredentialMax,
		Count:       len(out),
	})
}

// setRegistryCredential seals the plaintext PASSWORD and upserts
// the (app_id, registry) row. Quota is enforced before the seal so
// an over-cap request is rejected before any seal work happens.
// Idempotent: re-PUT replaces ciphertext + bumps updated_at.
//
// Hand-rolled phases, not a helper, because the line budget here
// is well under the §Conventions 50-line cap and the phase order
// matters for auditing (validate body → resolve app → check plan
// → check quota → seal → persist → log + audit).
func (s *server) setRegistryCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	var req api.PutAppRegistryCredentialRequest
	if err := decodeJSONSized(r, &req, maxRegistryBodyBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	host, err := normalizeRegistryHost(req.Registry)
	if err != nil {
		api.WriteProblem(w, api.ErrInvalidRegistryHost(err))
		return
	}
	req.Registry = host
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.RegistryCredentialMax == 0 {
		api.WriteProblem(w, api.ErrPlanRegistryCredentialsNotAllowed(acct.Plan))
		return
	}
	if prob := s.checkRegistryCredentialQuota(r.Context(), acct, app, host, limits); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.sealAndPersistRegistryCredential(r.Context(), acct, app, host, req.Username, req.Password); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Audit + log. PASSWORD never reaches slog. logsanitize.Field
	// is used for the host + username even though they're
	// metadata — defensive (a future refactor adding a
	// "request echo" log line won't leak).
	s.log.Info("registry credential set",
		"app", app.Slug,
		"account", acct.ID,
		"registry", logsanitize.Field(host),
		"username", logsanitize.Field(req.Username),
	)
	// IAM-4 (ADR-035): record the credential set. NEVER carry
	// the plaintext password in the audit row — same posture as
	// the secrets handler.
	s.audit.Emit(r.Context(), "registry_credential.set", &acct.ID, map[string]any{
		"app_id":   app.ID,
		"registry": host,
		"username": req.Username,
	})
	// Read back the stored row to surface the canonical
	// timestamps (server-side clock). Password NEVER echoed.
	row, err := s.store.GetAppRegistryCredential(r.Context(), acct.ID, app.ID, host)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read back credential"))
		return
	}
	writeJSON(w, http.StatusOK, toAppRegistryCredentialResponse(row))
}

// sealAndPersistRegistryCredential runs the "no-recipient / seal /
// persist" portion of setRegistryCredential. Pulled out so the
// handler itself reads as a sequence of guards, each calling a
// single check. Returns nil on success or a ready-to-write
// *api.Problem on failure.
//
// Sealing happens under namespace "registry_creds" (mirrors the
// app_secrets namespace "app_secret"). The same recipient is used
// as for app secrets — same file, same key. imaged's resolver
// unseals under the same namespace string and refuses blobs
// sealed under any other namespace (defence in depth).
func (s *server) sealAndPersistRegistryCredential(c stdctx, acct state.Account, app state.App, host, username, password string) *api.Problem {
	recipient := hostSecretRecipient()
	if recipient == nil {
		// Apid started without a host.age.pub; refuse to
		// accept plaintext.
		return api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	ciphertext, err := secretbox.SealBytes(recipient, "registry_creds", []byte(password), api.MaxRegistryPasswordBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return prob
		}
		return api.ErrCapacity("could not seal registry credential")
	}
	if err := s.store.UpsertAppRegistryCredential(c, acct.ID, app.ID, host, username, ciphertext); err != nil {
		return api.ErrCapacity("could not persist registry credential")
	}
	return nil
}

// checkRegistryCredentialQuota returns nil when a PUT for
// (app, host) is allowed under the per-plan RegistryCredentialMax,
// or a ready-to-write *api.Problem otherwise. Re-PUTs of an
// existing (app, host) are not new rows and so don't count
// against the quota — the existing row replaces in place.
//
// A nil *api.Problem means "proceed"; a non-nil one means
// "refuse, with this problem envelope". This shape keeps
// setRegistryCredential readable: it reads as a sequence of
// guards, each calling a single check.
//
// Round-trip cost: one query (RegistryCredentialQuotaCheck —
// CTE in PgStore, single-pass walk in MemStore) instead of
// CountAppRegistryCredentials + GetAppRegistryCredential. The
// (count, exists) shape mirrors the secrets handler's preflight
// contract.
func (s *server) checkRegistryCredentialQuota(c stdctx, acct state.Account, app state.App, host string, limits api.Limits) *api.Problem {
	n, exists, err := s.store.RegistryCredentialQuotaCheck(c, acct.ID, app.ID, host)
	if err != nil {
		return api.ErrCapacity("could not check registry credential quota")
	}
	if !exists && n >= limits.RegistryCredentialMax {
		return api.ErrPlanRegistryCredentialQuota(limits, n)
	}
	return nil
}

// deleteRegistryCredential removes the (app_id, registry) row.
// The registry host is a query param (?registry=<host>) — path
// segments don't escape cleanly when the host carries a :port.
// 404 CodeRegistryCredentialNotFound when the host isn't set —
// distinct from 403/404 because the URL resource IS the host.
func (s *server) deleteRegistryCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	host, err := normalizeRegistryHost(r.URL.Query().Get("registry"))
	if err != nil {
		api.WriteProblem(w, api.ErrInvalidRegistryHost(err))
		return
	}
	if host == "" {
		api.WriteProblem(w, api.ErrInvalidRegistryHost(errors.New("registry query parameter is required")))
		return
	}
	if err := s.store.DeleteAppRegistryCredential(r.Context(), acct.ID, app.ID, host); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrRegistryCredentialNotFound(host))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete registry credential"))
		return
	}
	s.log.Info("registry credential deleted",
		"app", app.Slug,
		"account", acct.ID,
		"registry", logsanitize.Field(host),
	)
	// IAM-4 (ADR-035): record the credential delete. No
	// password / ciphertext in the audit row.
	s.audit.Emit(r.Context(), "registry_credential.deleted", &acct.ID, map[string]any{
		"app_id":   app.ID,
		"registry": host,
	})
	w.WriteHeader(http.StatusNoContent)
}
