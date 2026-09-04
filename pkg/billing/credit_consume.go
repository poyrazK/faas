// Credit consumption reducer (issue #279 PR-C).
//
// The PR #337 / #279 PR-A surface only ISSUED credits; this package
// closes the loop by computing an invoice's overage in integer cents
// and draining the account's active credits FIFO against it.
//
// Today the only trigger is the operator endpoint
// POST /v1/invoices/{id}/consume-credits (cmd/apid/handlers_invoices_consume.go).
// Future callers — a meterd cron at month-rollover, the PR-B
// UpsertInvoice webhook Tx — call ConsumeCreditsForInvoice with their
// own actor string ("meterd", "apid-webhook"). The function never
// mutates the invoice row itself; that's PR-B's job. The reducer
// only writes to account_credits and credit_ledger.
//
// Money is integer cents end-to-end (CLAUDE.md). The overage math is shared
// with state.CurrentMonthOverageCents: the plan's included calendar-month
// allowance is removed first, then the remainder is priced at €0.01/GB-h.
// Floor division is performed at the final sub-cent boundary; the function
// never round-trips through float.
package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ComputeInvoiceOverageCents converts the account's usage_minutes for
// the invoice's billing period [PeriodStart, PeriodEnd) into integer
// cents of overage, floored. The plan allowance is applied once to the
// account aggregate, never once per app.
//
// Returns 0 when the account had no usage in the period (Free /
// Hobby under quota). The caller treats 0 as a no-op target: the
// reducer consumes zero credits and emits zero audit rows.
func ComputeInvoiceOverageCents(ctx context.Context, store state.Store, inv state.Invoice) (int64, error) {
	if inv.PeriodEnd.Before(inv.PeriodStart) {
		return 0, fmt.Errorf("billing: invoice %s has PeriodEnd %v before PeriodStart %v", inv.ID, inv.PeriodEnd, inv.PeriodStart)
	}
	acct, err := store.AccountByID(ctx, inv.AccountID)
	if err != nil {
		return 0, fmt.Errorf("billing: invoice account fetch: %w", err)
	}
	if inv.PeriodStart.IsZero() && inv.PeriodEnd.IsZero() {
		// Older operator-created invoice fixtures omitted the period. Preserve
		// that meaning as "all usage", but still apply the allowance once per
		// calendar month instead of once across the entire history.
		usages, err := store.UsageByAccount(ctx, inv.AccountID, time.Time{})
		if err != nil {
			return 0, fmt.Errorf("billing: invoice overage usage fetch: %w", err)
		}
		byMonth := make(map[string]int64)
		for _, usage := range usages {
			byMonth[usage.Month.UTC().Format("2006-01")] += usage.MBSeconds
		}
		var billableMBSeconds int64
		for _, total := range byMonth {
			billableMBSeconds += api.BillableMBSeconds(acct.Plan, total)
		}
		return api.OverageCentsForBillableMBSeconds(billableMBSeconds), nil
	}
	if inv.PeriodStart.IsZero() || inv.PeriodEnd.IsZero() {
		return 0, fmt.Errorf("billing: invoice %s requires both PeriodStart and PeriodEnd", inv.ID)
	}
	billableMBSeconds, err := OverageMBSecondsForRange(ctx, store, acct, inv.PeriodStart, inv.PeriodEnd)
	if err != nil {
		return 0, fmt.Errorf("billing: invoice overage usage fetch: %w", err)
	}
	return api.OverageCentsForBillableMBSeconds(billableMBSeconds), nil
}

// ConsumeCreditsForInvoice is the provider-neutral reducer. It looks
// up the invoice, computes overage cents from usage_minutes for the
// invoice's billing period, drains active credits FIFO, and returns
// the result.
//
// The store's ConsumeAccountCredit handles the per-credit UPDATE /
// INSERT and the dedupe on provider_invoice_id; the reducer is a thin
// orchestrator on top of that primitive. It returns the Invoice so the
// caller can stamp the audit row's provider_invoice_id and period_end
// fields without re-fetching.
//
// actor parameter lets the caller stamp the system identity on the
// consumption ledger row. Today: "apid" (the admin endpoint). Future
// callers stamp "meterd" (cron) or "apid-webhook" (PR-B Tx).
// reason parameter rides on each credit_ledger row's reason column
// for traceability; the per-credit audit row's reason is the operator
// text from POST /v1/admin/accounts/{id}/credits.
//
// Safe to call from: apid admin endpoint (today), meterd cron
// (future), PR-B webhook Tx (future). The function never mutates
// the invoice row.
func ConsumeCreditsForInvoice(ctx context.Context, store state.Store, invoiceID, actor, reason string) (state.ConsumeAccountCreditResult, state.Invoice, error) {
	if invoiceID == "" {
		return state.ConsumeAccountCreditResult{}, state.Invoice{}, errors.New("billing: ConsumeCreditsForInvoice: invoiceID required")
	}
	inv, err := store.GetInvoiceByID(ctx, invoiceID)
	if err != nil {
		return state.ConsumeAccountCreditResult{}, state.Invoice{}, fmt.Errorf("billing: invoice lookup: %w", err)
	}

	target, err := ComputeInvoiceOverageCents(ctx, store, inv)
	if err != nil {
		return state.ConsumeAccountCreditResult{}, state.Invoice{}, fmt.Errorf("billing: compute overage: %w", err)
	}

	res, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{
		AccountID:         inv.AccountID,
		TargetCents:       target,
		Provider:          inv.Provider,
		ProviderInvoiceID: inv.ProviderInvoiceID,
		InvoiceID:         inv.ID,
		Reason:            reason,
		Actor:             actor,
	})
	if err != nil {
		return state.ConsumeAccountCreditResult{}, inv, fmt.Errorf("billing: consume: %w", err)
	}
	return res, inv, nil
}
