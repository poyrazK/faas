package sched

// adr: 633
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
		_, err := r.Retire(attemptCtx, row.Source.InstanceKey, p, row.FenceID)
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
	if log == nil {
		log = slog.Default()
	}
	var after state.RegionalRuntimeRetirementCursor
	ticker := time.NewTicker(api.RegionalRuntimeRetirementInterval)
	defer ticker.Stop()
	for {
		next, completed, err := r.RecoverPage(ctx, after)
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
