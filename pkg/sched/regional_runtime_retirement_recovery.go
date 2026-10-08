package sched

// adr: 633, 649
import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// RecoverPage retries only retained original sources with a durable cell fence.
// A failed item advances the cursor so an unreachable host cannot starve other
// attempts. An empty page resets the scan; failures are retried next traversal.
func (r RegionalRuntimeRetirement) RecoverPage(ctx context.Context, after state.RegionalRuntimeRetirementCursor) (state.RegionalRuntimeRetirementCursor, int, error) {
	return r.recoverPage(ctx, after, nil)
}

func (r RegionalRuntimeRetirement) recoverPage(ctx context.Context, after state.RegionalRuntimeRetirementCursor, complete func(context.Context, runtimequota.LaunchSource, state.RegionalRuntimeCapacityReceipt) error) (state.RegionalRuntimeRetirementCursor, int, error) {
	if r.Authority.Store == nil || r.Router == nil || r.Router.regionalRuntimePeers == nil || r.Authority.Cells.CheckSigner(r.Signer, runtimequota.Cell) != nil {
		return after, 0, runtimequota.ErrTrust
	}
	rows, err := r.Authority.Store.ListRegionalRuntimeRetirements(ctx, r.Signer.IssuerID(), after, api.RegionalRuntimeRetirementBatch)
	if err != nil {
		return after, 0, err
	}
	if len(rows) == 0 {
		return state.RegionalRuntimeRetirementCursor{}, 0, nil
	}
	completed := 0
	var lastErr error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return after, completed, err
		}
		p := row.Source.Permit
		attemptCtx, cancel := context.WithTimeout(ctx, api.RegionalRuntimeRetirementTimeout)
		receipt, err := r.Retire(attemptCtx, row.Source.InstanceKey, p, row.FenceID)
		if err == nil && complete != nil {
			err = complete(attemptCtx, row.Source, receipt)
		}
		cancel()
		if err != nil {
			lastErr = err
		} else {
			completed++
		}
		after = state.RegionalRuntimeRetirementCursor{AppID: p.Grant.Binding.RegionalAppID, PermitID: p.ID}
	}
	return after, completed, lastErr
}

// RunRecovery owns no mutable scheduler rows and performs no RPC in a database
// transaction. Shutdown cancels the active bounded attempt before returning.
func (r RegionalRuntimeRetirement) RunRecovery(ctx context.Context, log *slog.Logger) error {
	return r.runRecovery(ctx, log, r.RecoverPage)
}

func (r RegionalRuntimeRetirement) runRecovery(ctx context.Context, log *slog.Logger, recoverPage func(context.Context, state.RegionalRuntimeRetirementCursor) (state.RegionalRuntimeRetirementCursor, int, error)) error {
	if log == nil {
		log = slog.Default()
	}
	var after state.RegionalRuntimeRetirementCursor
	ticker := time.NewTicker(api.RegionalRuntimeRetirementInterval)
	defer ticker.Stop()
	for {
		next, completed, err := recoverPage(ctx, after)
		after = next
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Warn("schedd: original regional retirement recovery holds retained", "cell_id", r.Signer.IssuerID(), "err", err)
		}
		if completed > 0 {
			log.Info("schedd: original regional retirements completed", "cell_id", r.Signer.IssuerID(), "count", completed)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RecoverRegionalRuntimeFailures completes scheduler effects only after an
// accepted original stop. Released receipts still awaiting a matching active
// row are retried without another native RPC.
func (e *Engine) RecoverRegionalRuntimeFailures(ctx context.Context, after state.RegionalRuntimeRetirementCursor) (state.RegionalRuntimeRetirementCursor, int, error) {
	e.mu.Lock()
	owner := e.regionalRetirement
	e.mu.Unlock()
	if owner == nil {
		return after, 0, runtimequota.ErrTrust
	}
	return owner.recoverPage(ctx, after, func(ctx context.Context, source runtimequota.LaunchSource, receipt state.RegionalRuntimeCapacityReceipt) error {
		return e.completeOriginalRegionalFailure(ctx, source, receipt, "regional_retirement_recovered")
	})
}

func (e *Engine) RunRegionalRuntimeFailureRecovery(ctx context.Context, log *slog.Logger) error {
	e.mu.Lock()
	owner := e.regionalRetirement
	e.mu.Unlock()
	if owner == nil {
		return runtimequota.ErrTrust
	}
	if log == nil {
		log = slog.Default()
	}
	var inventoryAfter state.RegionalRuntimeRetirementCursor
	return owner.runRecovery(ctx, log, func(ctx context.Context, after state.RegionalRuntimeRetirementCursor) (state.RegionalRuntimeRetirementCursor, int, error) {
		next, completed, err := e.RecoverRegionalRuntimeFailures(ctx, after)
		if ctx.Err() == nil {
			var report RegionalRuntimeInventoryReport
			var observedErr error
			inventoryAfter, report, observedErr = e.ObserveRegionalRuntimeInventoryPage(ctx, inventoryAfter)
			if observedErr != nil || len(report.Issues) > 0 || len(report.CurrentIssues) > 0 || report.NativeUnresolved > 0 {
				log.Warn("schedd: regional native inventory unresolved; holds retained", "matched", len(report.Matches), "current_attempts", len(report.CurrentAttempts), "current_attempt_issues", len(report.CurrentIssues), "unresolved_holds", len(report.Issues), "native_unresolved", report.NativeUnresolved, "nodes", report.Nodes, "err", observedErr)
			} else if report.Nodes > 0 {
				log.Info("schedd: regional original holds observed; admission remains frozen", "matched", len(report.Matches), "current_attempts", len(report.CurrentAttempts), "nodes", report.Nodes)
			}
		}
		return next, completed, err
	})
}
