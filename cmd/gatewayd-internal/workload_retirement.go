package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the authoritative scoped admission lookup on the production store
// adapter; the gateway's weight projection alone carries no retirement state.
func (a weightsStoreAdapter) EnvironmentWorkloadAdmissionRetired(ctx context.Context, appID, scope string) (bool, error) {
	store, ok := a.store.(state.EnvironmentWorkloadRetirementAdmissionStore)
	if !ok {
		return false, errors.New("scoped workload admission store unavailable")
	}
	return store.EnvironmentWorkloadAdmissionRetired(ctx, appID, scope)
}
