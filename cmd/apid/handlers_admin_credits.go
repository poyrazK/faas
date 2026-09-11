// Issue #279 — operator-facing credit issuance surface.
//
// Endpoints:
//
//	POST /v1/admin/accounts/{id}/credits   — issue an account credit (admin-only)
//
// Auth model: admin-only. The route sits behind the
// `requireScope(api.ScopesAdminOnly...)` middleware AND the email
// allowlist loaded from FAAS_ADMIN_EMAILS (same two-layer gate as
// /v1/compute-nodes). The middleware keeps the scope check
// declarative; the allowlist is what stops a leaked admin key from
// a non-operator account from issuing credits.
//
// Idempotency: the existing `idempotent` middleware (24-h dedupe
// keyed on the authenticated caller account) replays a prior 201
// without re-issuing. The CLI supplies a stable `cli-admin-credit-…`
// key so a flaky-network retry returns the same credit_id.
//
// Money: integer cents (CLAUDE.md "never float on money"). 0 is
// rejected; negative is rejected; > 0 is the only accepted shape.
//
// Audit: the credit row is the source of truth; the credit_ledger
// row is observational (a separate statement). If the ledger INSERT
// fails the credit still lands, the handler logs a WARN, and the
// audit.Emit on `credit.issued` carries the operator's account ID
// in `data["actor"]` so an operator can correlate without
// re-deriving from logs.

package main

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// issueCreditPayload is the JSON body POST /v1/admin/accounts/{id}/credits
// accepts. Reason is required and length-validated client-side for a
// friendlier 400; the migration's CHECK constraint is the floor a
// typo cannot cross. Cents is integer, > 0, in EUR cents.
type issueCreditPayload struct {
	Cents  int64  `json:"cents"`
	Reason string `json:"reason"`
}

// issueCredit handles POST /v1/admin/accounts/{id}/credits. Returns
// 201 on success, 400 on validation, 403 on the admin allowlist, 404
// on unknown target account. The idempotency middleware wraps the
// 201; a duplicate Idempotency-Key returns the original response.
func (s *server) issueCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if s.billingProvider == nil || !s.billingProvider.Capabilities().Has(billing.CapRefund) {
		api.WriteProblem(w, api.ErrBillingNotImplemented(
			"credits require a billing provider with idempotent refund support"))
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad account id", "expected UUID"))
		return
	}
	var req issueCreditPayload
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad JSON", err.Error()))
		return
	}
	if req.Cents <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad cents", "cents must be a positive integer (in EUR cents)"))
		return
	}
	if n := len(req.Reason); n < 3 || n > 500 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad reason", "reason must be 3..500 characters"))
		return
	}
	if _, err := s.store.AccountByID(r.Context(), targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Account not found", err.Error()))
		return
	}
	credit, err := s.store.CreateAccountCredit(r.Context(), state.AccountCredit{
		AccountID:      targetID,
		CentsRemaining: req.Cents,
		Reason:         req.Reason,
		// ExpiresAt is intentionally never set on issuance — the
		// operator cannot set expires_at via the API today; the
		// column is reserved for a future consumption-reducer follow-up
		// that may want to attach an auto-expiry to a goodwill credit.
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create credit"))
		return
	}
	if err := s.store.CreateCreditLedgerEntry(r.Context(), state.CreditLedgerEntry{
		AccountID:  targetID,
		CreditID:   credit.ID,
		DeltaCents: req.Cents,
		Reason:     req.Reason,
		Actor:      acct.ID,
	}); err != nil {
		// The credit row is the source of truth; do not roll back.
		// The ledger insert is observational and retryable.
		s.log.Warn("credit ledger write failed",
			"credit_id", credit.ID,
			"account_id", targetID,
			"err", err)
	}
	tid := targetID
	s.audit.Emit(r.Context(), "credit.issued", &tid, map[string]any{
		"credit_id":   credit.ID,
		"cents":       req.Cents,
		"actor":       acct.ID,
		"actor_email": acct.Email,
		"reason":      logsanitize.Field(req.Reason),
	})
	writeJSON(w, http.StatusCreated, api.AccountCreditResponse{
		ID:             credit.ID,
		AccountID:      credit.AccountID,
		CentsRemaining: credit.CentsRemaining,
		Reason:         credit.Reason,
		CreatedAt:      credit.CreatedAt,
		ExpiresAt:      credit.ExpiresAt,
	})
}
