package sched

// adr: 616
import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This check never consults the directory and never activates an allowance.
// Repeating it at execution boundaries narrows races; it does not replace a
// durable reservation or prove teardown of work already running.
func checkRegionalRuntimeQuota(ctx context.Context, store state.Store, appID string) error {
	if store == nil {
		return state.ErrRegionalRuntimeQuotaFrozen
	}
	err := state.CheckRegionalRuntimeQuotaAdmission(ctx, store, appID)
	if errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
		return errors.Join(err, api.NewProblem(503, api.CodeCapacity,
			"Regional runtime capacity unavailable", "the regional runtime quota is not admitted"))
	}
	if err != nil {
		return fmt.Errorf("sched: regional runtime quota: %w", err)
	}
	return nil
}
