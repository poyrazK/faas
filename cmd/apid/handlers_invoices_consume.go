// Issue #279 PR-C — credit consumption reducer trigger.
//
// Endpoint:
//
//	POST /v1/invoices/{id}/consume-credits   — admin-only, MFA-gated, idempotent
//
// Today the reducer is operator-triggered at month-rollover. The same
// `pkg/billing.ConsumeCreditsForInvoice` will be called by:
//
//   - the PR-B `UpsertInvoice` webhook Tx (one-line call from inside
//     the Tx; no contract change for the reducer)
//   - a future meterd cron (operator's actor string changes to
//     "meterd"; same function)
//
// Auth model: admin-only, two-layer gate (requireScope +
// adminAllows email allowlist) plus requireMFA — the spec §11 ship
// blocker says MFA is mandatory for any operator action that
// mutates money. Asymmetric with the issuance endpoint (which
// doesn't require MFA) — explicit and accepted per the PR-C plan
// (decision D5).
//
// Idempotency: the route is wrapped by the existing `idempotent`
// middleware (24-h dedupe keyed on the authenticated caller
// account). The reducer itself is also idempotent at the DB level
// via the partial unique index on credit_ledger
// (provider_invoice_id, credit_id) — see migration
// 00058_credit_consumption.sql. A second reducer call for the same
// invoice returns AlreadyConsumedForInvoice=true and the same
// ConsumedCents without double-decrementing.
//
// Money: integer cents (CLAUDE.md: never float on money). The
// reducer reports the plan-aware integer-cent overage after the included
// calendar-month allowance has been removed.
//
// Audit: one `credit.consumed` row per drained credit (subject =
// beneficiary account, actor = "apid", data carries invoice_id,
// provider_invoice_id, period_end, and the totals so a SOC 2 reader
// can correlate without re-fetching).

package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// consumeInvoiceCredits handles POST /v1/invoices/{id}/consume-credits.
// Returns 200 on a fresh drain, 200 with already_consumed_for_invoice=true
// on an idempotent replay, 400 on bad invoice id, 403 on the admin
// allowlist, 404 on unknown invoice, 500 on reducer error.
//
// The 50-line handler invariant is preserved: adminAllows → uuid
// parse → reducer → audit emit loop → writeJSON. Each step is one
// logical block.
func (s *server) consumeInvoiceCredits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	invoiceID := r.PathValue("id")
	if _, err := uuid.Parse(invoiceID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad invoice id", "expected UUID"))
		return
	}

	const actor = "apid"
	const reason = "admin endpoint: POST /v1/invoices/{id}/consume-credits"
	res, inv, err := billing.ConsumeCreditsForInvoice(r.Context(), s.store, invoiceID, actor, reason)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Invoice not found", err.Error()))
			return
		}
		// Reducer error is not a "deliberate refusal" (which would
		// be ErrCapacity / 503); it's an unexpected server-side
		// failure — DB commit, network blip, partial state. 500 with
		// the verbatim err.Error() surfaces the breadcrumb in the
		// operator's browser console; the audit row carries the
		// same text for the on-call engineer.
		api.WriteProblem(w, api.ErrInternal(err.Error()))
		return
	}

	// One credit.consumed audit row per drained credit. The reducer
	// already wrote the credit_ledger rows; the audit row carries the
	// invoice + totals so a SOC 2 reader can correlate without
	// re-deriving.
	for _, row := range res.PerCredit {
		tid := inv.AccountID
		s.audit.Emit(r.Context(), "credit.consumed", &tid, map[string]any{
			"credit_id":                        row.CreditID,
			"delta_cents":                      row.DeltaCents,
			"invoice_id":                       inv.ID,
			"provider_invoice_id":              inv.ProviderInvoiceID,
			"period_end":                       inv.PeriodEnd,
			"total_consumed_cents_for_invoice": res.ConsumedCents,
			"remaining_credits_cents":          res.RemainingCreditsCents,
		})
	}

	writeJSON(w, http.StatusOK, api.ConsumeInvoiceResponse{
		InvoiceID:                 inv.ID,
		ConsumedCents:             res.ConsumedCents,
		RemainingCreditsCents:     res.RemainingCreditsCents,
		AlreadyConsumedForInvoice: res.AlreadyConsumedForInvoice,
		PerCredit:                 mapConsumedRows(res.PerCredit),
	})
}

// mapConsumedRows converts state-level ConsumedCreditRow to the wire
// shape. Kept as a tiny helper so the handler stays one logical
// statement per step. Parity helper; not a business-logic seam.
func mapConsumedRows(in []state.ConsumedCreditRow) []api.ConsumedCreditRow {
	if in == nil {
		return nil
	}
	out := make([]api.ConsumedCreditRow, len(in))
	for i, r := range in {
		out[i] = api.ConsumedCreditRow{
			CreditID:   r.CreditID,
			DeltaCents: r.DeltaCents,
			NewBalance: r.NewBalance,
		}
	}
	return out
}
