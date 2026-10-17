package billingruns

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// billConsumer drafts, checks, finalizes and exports one app consumer's
// month (ADR-1503). It sets the item's outcome, or returns a transient
// problem to retry.
func (s *Service) billConsumer(ctx context.Context, acct state.Account, run state.BillingRun, item *state.BillingRunItem) *api.Problem {
	consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, acct.ID, item.SourceID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && consumer.AppID != item.AppID) {
		item.Outcome, item.Reason = state.BillingRunItemFailed, "the consumer no longer exists"
		return nil
	}
	if err != nil {
		return api.ErrInternal("could not load the consumer")
	}
	statement, problem := s.Host.DraftConsumerStatement(ctx, acct, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
	if problem != nil {
		return settle(item, state.BillingRunItemHeld, problem)
	}
	recordStatement(item, statement.ID, statement.Revision, statement.Currency, statement.AmountMillicents)
	newUsage := statement.Status != state.APIConsumerUsageStatementFinalized
	if newUsage {
		if blocked, problem := s.handoffBlocked(ctx, acct, state.StatementSourceAppConsumer, statement.ID); problem != nil {
			return problem
		} else if blocked {
			item.Outcome, item.Reason = state.BillingRunItemSkipped, overlapSkipReason
			return nil
		}
		if held, problem := s.checkCompleteness(ctx, acct, run, item); problem != nil || held {
			return problem
		}
		if run.Finalize {
			final, problem := s.Host.FinalizeConsumerStatement(ctx, acct, item.AppID, item.SourceID, statement.ID)
			if problem != nil {
				return settle(item, state.BillingRunItemHeld, problem)
			}
			recordStatement(item, final.ID, final.Revision, final.Currency, final.AmountMillicents)
		}
	}
	if run.Export != state.BillingRunExportNone {
		if done, problem := s.exportConsumer(ctx, acct, consumer, run, item); problem != nil || done {
			return problem
		}
	}
	finish(item, run, newUsage)
	return nil
}

// checkCompleteness records the completeness status and holds the item on
// a detected gap when the run asks to.
func (s *Service) checkCompleteness(ctx context.Context, acct state.Account, run state.BillingRun, item *state.BillingRunItem) (bool, *api.Problem) {
	status, missing, err := s.Host.ConsumerCompleteness(ctx, acct, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		return false, api.ErrInternal("could not compare usage with request telemetry")
	}
	item.Completeness = status
	if !run.HoldOnGaps || status != billing.CompletenessGapsDetected {
		return false, nil
	}
	item.Outcome = state.BillingRunItemHeld
	item.Reason = fmt.Sprintf("request telemetry saw at least %d successful requests the usage ledger did not bill; reconcile with `gregale consumers completeness` before invoicing", missing)
	return true, nil
}

// exportConsumer exports every finalized revision of the month that has no
// handoff, oldest first. done is true when it set a terminal outcome.
func (s *Service) exportConsumer(ctx context.Context, acct state.Account, consumer state.APIConsumer, run state.BillingRun,
	item *state.BillingRunItem) (bool, *api.Problem) {
	statements, okStatements := s.Host.Store().(state.APIConsumerUsageStatementStore)
	handoffs, okHandoffs := s.Host.Store().(state.APIConsumerUsageStatementHandoffStore)
	if !okStatements || !okHandoffs {
		return false, api.ErrInternal("API consumer usage statements are unavailable")
	}
	revisions, err := statements.ListAPIConsumerUsageStatementRevisions(ctx, acct.ID, item.AppID, item.SourceID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		return false, api.ErrInternal("could not list the month's statements")
	}
	for _, revision := range revisions {
		if revision.Status != state.APIConsumerUsageStatementFinalized || revision.AmountDueMillicents() == 0 {
			if revision.Status == state.APIConsumerUsageStatementFinalized && revision.CreditAppliedMillicents > 0 && item.ExportedCount == 0 {
				item.Reason = creditCoveredReason
			}
			continue
		}
		if _, err := handoffs.GetAPIConsumerUsageStatementHandoff(ctx, acct.ID, item.AppID, item.SourceID, revision.ID); err == nil {
			continue
		} else if !errors.Is(err, state.ErrNotFound) {
			return false, api.ErrInternal("could not load the statement handoff")
		}
		if blocked, problem := s.handoffBlocked(ctx, acct, state.StatementSourceAppConsumer, revision.ID); problem != nil || blocked {
			if blocked {
				item.Outcome, item.Reason = state.BillingRunItemSkipped, overlapSkipReason
			}
			return blocked, problem
		}
		full, err := statements.GetAPIConsumerUsageStatement(ctx, acct.ID, item.AppID, item.SourceID, revision.ID)
		if err != nil {
			return false, api.ErrInternal("could not load the statement")
		}
		export, problem := s.Host.ExportConsumerStatement(ctx, acct, consumer, full, run.Export)
		if problem != nil {
			return true, settle(item, state.BillingRunItemFailed, problem)
		}
		item.InvoiceID, item.ExportedCount, item.Reason = export.ProviderInvoiceID, item.ExportedCount+1, ""
	}
	return false, nil
}

// billTenant drafts, finalizes and exports one platform tenant's month.
func (s *Service) billTenant(ctx context.Context, acct state.Account, run state.BillingRun, item *state.BillingRunItem) *api.Problem {
	tenants, okTenants := s.Host.Store().(state.PlatformTenantStore)
	if !okTenants {
		return api.ErrInternal("platform tenants are unavailable")
	}
	tenant, err := tenants.GetPlatformTenant(ctx, acct.ID, item.SourceID)
	if errors.Is(err, state.ErrNotFound) {
		item.Outcome, item.Reason = state.BillingRunItemFailed, "the platform tenant no longer exists"
		return nil
	}
	if err != nil {
		return api.ErrInternal("could not load the platform tenant")
	}
	statement, problem := s.Host.DraftTenantStatement(ctx, acct, tenant.ID, run.PeriodStart, run.PeriodEnd)
	if problem != nil {
		return settle(item, state.BillingRunItemHeld, problem)
	}
	recordStatement(item, statement.ID, statement.Revision, statement.Currency, statement.AmountMillicents)
	newUsage := statement.Status != state.APIConsumerUsageStatementFinalized
	if newUsage {
		if blocked, problem := s.handoffBlocked(ctx, acct, state.StatementSourcePlatformTenant, statement.ID); problem != nil {
			return problem
		} else if blocked {
			item.Outcome, item.Reason = state.BillingRunItemSkipped, overlapSkipReason
			return nil
		}
		if run.Finalize {
			final, problem := s.Host.FinalizeTenantStatement(ctx, acct, tenant.ID, statement.ID)
			if problem != nil {
				return settle(item, state.BillingRunItemHeld, problem)
			}
			recordStatement(item, final.ID, final.Revision, final.Currency, final.AmountMillicents)
		}
	}
	if run.Export != state.BillingRunExportNone {
		if done, problem := s.exportTenant(ctx, acct, tenant, run, item); problem != nil || done {
			return problem
		}
	}
	finish(item, run, newUsage)
	return nil
}

// exportTenant exports every finalized tenant revision of the month that
// has no handoff, oldest first. done is true when it set a terminal outcome.
func (s *Service) exportTenant(ctx context.Context, acct state.Account, tenant state.PlatformTenant, run state.BillingRun,
	item *state.BillingRunItem) (bool, *api.Problem) {
	statements, ok := s.Host.Store().(state.PlatformTenantStatementStore)
	if !ok {
		return false, api.ErrInternal("platform tenant statements are unavailable")
	}
	revisions, err := statements.ListPlatformTenantStatements(ctx, acct.ID, tenant.ID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		return false, api.ErrInternal("could not list the month's statements")
	}
	for _, revision := range revisions {
		if revision.Status != state.APIConsumerUsageStatementFinalized || revision.AmountMillicents == 0 {
			continue
		}
		if _, err := statements.GetPlatformTenantStatementHandoff(ctx, acct.ID, tenant.ID, revision.ID); err == nil {
			continue
		} else if !errors.Is(err, state.ErrNotFound) {
			return false, api.ErrInternal("could not load the statement handoff")
		}
		if blocked, problem := s.handoffBlocked(ctx, acct, state.StatementSourcePlatformTenant, revision.ID); problem != nil || blocked {
			if blocked {
				item.Outcome, item.Reason = state.BillingRunItemSkipped, overlapSkipReason
			}
			return blocked, problem
		}
		full, err := statements.GetPlatformTenantStatement(ctx, acct.ID, tenant.ID, revision.ID)
		if err != nil {
			return false, api.ErrInternal("could not load the statement")
		}
		export, problem := s.Host.ExportTenantStatement(ctx, acct, tenant, full, run.Export)
		if problem != nil {
			return true, settle(item, state.BillingRunItemFailed, problem)
		}
		item.InvoiceID, item.ExportedCount = export.ProviderInvoiceID, item.ExportedCount+1
	}
	return false, nil
}
