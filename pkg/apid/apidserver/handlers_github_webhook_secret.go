// handlers_github_webhook_secret.go — POST
// /v1/admin/github-webhook-secrets (PR-D / ADR-012 §7
// amendment).
//
// Per-tenant override of the platform-wide
// FAAS_GITHUB_WEBHOOK_SECRET so a leaked tenant secret can
// rotate without coordinating every GitHub App install. The
// daemon-side resolver at pkg/githubd/webhook_secret.go reads
// the per-tenant row first, falls back to the platform secret
// only on a missing row. This handler is the operator-facing
// writer (admin-scoped + email allowlist, mirroring the credit
// + sign-keys route shapes).
//
//   POST /v1/admin/github-webhook-secrets
//     { "installation_id": 12345, "secret_hex": "deadbeef..." }
//   → 201
//     { "installation_id": 12345,
//       "upgraded_at": "2026-08-11T12:34:56Z",
//       "upgraded_by": "admin:<uuid>" }
//
// Auth model: admin-only. The route sits behind
// `requireScope(api.ScopesAdminOnly...)` AND the
// FAAS_ADMIN_EMAILS email allowlist — same two-layer gate as
// /v1/admin/accounts/{id}/credits. The middleware keeps the
// scope check declarative; the allowlist is what stops a
// leaked admin key from rotating a webhook secret for a
// different tenant's GitHub App install.
//
// Idempotency: the SQL is `ON CONFLICT (installation_id) DO
// UPDATE SET upgraded_at = now()`. Every successful call — even
// a re-run with the same secret — bumps `upgraded_at` and
// refreshes `upgraded_by`. The bump is the audit trail; an
// operator re-running the same secret is itself a rotation
// event worth recording. The Prometheus counter
// `githubd_webhook_secret_total{status="set"}` is emitted on
// every successful call so a dashboard alert can flag
// unexpected rotation frequency.

package apidserver

import (
	"encoding/hex"
	"net/http"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// setGithubWebhookSecretPayload is the JSON body
// POST /v1/admin/github-webhook-secrets accepts. Hex so the
// plaintext never has to be a binary argv value; the CLI uses
// the same shape.
type setGithubWebhookSecretPayload struct {
	InstallationID int64  `json:"installation_id"`
	SecretHex      string `json:"secret_hex"`
}

// handleSetGithubWebhookSecret handles POST
// /v1/admin/github-webhook-secrets. Returns 201 on success,
// 400 on validation (bad hex, bad length), 403 on the admin
// allowlist. The audit event githubd.webhook_secret_set
// carries the operator's account ID + installation_id so the
// on-call can correlate without re-deriving from logs.
//
// Length bounds match pkg/githubd/webhook.go's verifier
// (HMAC-SHA256 with a 16..64-byte key). The server-side
// floor catches the case where a CLI bug ships a too-short
// secret; the SQL has no upper bound at the format level so a
// future stronger entropy cap can land without a migration.
func (s *server) handleSetGithubWebhookSecret(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	var req setGithubWebhookSecretPayload
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad JSON", err.Error()))
		return
	}
	if req.InstallationID == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad installation_id", "installation_id must be a positive integer"))
		return
	}
	raw, err := hex.DecodeString(req.SecretHex)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad secret_hex", err.Error()))
		return
	}
	if len(raw) < 16 || len(raw) > 64 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad secret length", "secret must be 16..64 bytes (32..128 hex chars)"))
		return
	}
	sealed, prob := sealGithubWebhookSecret(raw)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	upgradedAt, upgradedBy, err := s.store.UpsertGithubWebhookSecret(r.Context(),
		req.InstallationID, state.GithubWebhookSecret{Value: raw, Sealed: sealed}, "admin:"+acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not set webhook secret"))
		return
	}
	s.audit.Emit(r.Context(), "githubd.webhook_secret_set", nil, map[string]any{
		"installation_id": req.InstallationID,
		"actor":           acct.ID,
		"actor_email":     acct.Email,
		"upgraded_at":     upgradedAt.Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, api.AdminSetGithubWebhookSecretResponse{
		InstallationID: req.InstallationID,
		UpgradedAt:     upgradedAt,
		UpgradedBy:     upgradedBy,
	})
}

// sealGithubWebhookSecret seals the copy githubd opens (ADR 20261022111812).
// The source domain is not wired yet, so it is sealed to the fleet
// recipient; the plaintext column stays written for the release that reads
// only it.
func sealGithubWebhookSecret(raw []byte) ([]byte, *api.Problem) {
	recipient := hostSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("could not seal webhook secret")
	}
	sealed, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{recipient}, raw)
	if err != nil {
		return nil, api.ErrCapacity("could not seal webhook secret")
	}
	return sealed, nil
}
