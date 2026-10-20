package billingruns

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Product Control item reasons (ADR-20261018212918).
const (
	productDisabledReason = "Product Control is disabled; start another run for this month once it is enabled"
	productHandoffReason  = "already handed off by hand: the owner recorded an external invoice for this statement"
	productNoReceipts     = "nothing to bill: no Product Control receipts in this month"
)

// billProductCustomer bills one Product Control customer's month: it reviews
// the month, holds the item when it cannot be finalized, finalizes the
// reviewed charges and exports the statement. A review that changes between
// steps is retried; a held item is billed by a later run for the month once
// the owner reconciles or waives its receipts.
func (s *Service) billProductCustomer(ctx context.Context, acct state.Account, run state.BillingRun, item *state.BillingRunItem) *api.Problem {
	if !s.Host.ProductControlEnabled() {
		item.Outcome, item.Reason = state.BillingRunItemHeld, productDisabledReason
		return nil
	}
	billing, ok := s.Host.Store().(state.ProductBillingStore)
	if !ok {
		return api.ErrInternal("Product Control billing is unavailable")
	}
	scope := state.ProductBillingScope{AccountID: acct.ID, AppID: item.AppID, TenantID: item.SourceID, Period: run.PeriodStart.UTC().Format("2006-01")}
	review, err := billing.ReviewProductBilling(ctx, scope)
	if errors.Is(err, state.ErrNotFound) {
		item.Outcome, item.Reason = state.BillingRunItemFailed, "the Product Control app, catalog or customer no longer exists"
		return nil
	}
	if err != nil {
		return api.ErrInternal("could not review the customer's month")
	}
	statement, newUsage := review.Finalized, review.Finalized == nil
	if newUsage {
		if held := productHold(review); held != "" {
			item.Outcome, item.Reason = state.BillingRunItemHeld, held
			return nil
		}
		if review.Starts == 0 {
			item.Outcome, item.Reason = state.BillingRunItemUpToDate, productNoReceipts
			return nil
		}
		item.Currency, item.AmountMillicents = review.Currency, review.AmountMillicents
		if run.Finalize {
			final, problem := s.Host.FinalizeProductStatement(ctx, acct, scope, review.ReviewHash)
			if problem != nil && problem.Code == "product_billing_review_changed" {
				return api.NewProblem(http.StatusServiceUnavailable, problem.Code, problem.Title, "the month's receipts changed during the run")
			}
			if problem != nil {
				return settle(item, state.BillingRunItemHeld, problem)
			}
			statement = &final
		}
	}
	if statement != nil {
		recordStatement(item, statement.ID, 0, statement.Currency, statement.AmountMillicents)
	}
	if run.Export != state.BillingRunExportNone && statement != nil {
		if done, problem := s.exportProductStatement(ctx, acct, billing, scope, *statement, run, item); problem != nil || done {
			return problem
		}
	}
	finish(item, run, newUsage)
	return nil
}

// productHold names why an unfinalized month cannot be finalized, with the
// review's problem code.
func productHold(review api.ProductBillingReview) string {
	switch {
	case !review.Closed:
		return "product_billing_period_open: only a closed UTC month can be finalized"
	case review.Unresolved > 0:
		return fmt.Sprintf("product_billing_unresolved: %d runs have no recorded outcome; reconcile or waive them, then start another run for this month",
			review.Unresolved)
	}
	return ""
}

// exportProductStatement exports a finalized statement that is neither
// exported nor handed off by hand; an unfinished export resumes. done is
// true when it set a terminal outcome.
func (s *Service) exportProductStatement(ctx context.Context, acct state.Account, billing state.ProductBillingStore, scope state.ProductBillingScope,
	statement api.ProductBillingDocument, run state.BillingRun, item *state.BillingRunItem) (bool, *api.Problem) {
	if statement.AmountMillicents == 0 {
		return false, nil
	}
	exports, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return false, api.ErrInternal("statement invoice exports are unavailable")
	}
	export, err := exports.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, statement.ID)
	switch {
	case err == nil && export.Status == state.StatementInvoiceExportCompleted:
		return false, nil
	case err != nil && !errors.Is(err, state.ErrNotFound):
		return false, api.ErrInternal("could not load the statement's export")
	case errors.Is(err, state.ErrNotFound):
		if _, err := billing.GetProductBillingHandoff(ctx, scope, statement.ID); err == nil {
			item.Outcome, item.Reason = state.BillingRunItemSkipped, productHandoffReason
			return true, nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return false, api.ErrInternal("could not load the statement handoff")
		}
	}
	result, problem := s.Host.ExportProductStatement(ctx, acct, scope, statement.ID, run.Export)
	if problem != nil {
		return true, settle(item, state.BillingRunItemFailed, problem)
	}
	item.InvoiceID, item.ExportedCount = result.InvoiceID, item.ExportedCount+1
	return false, nil
}
