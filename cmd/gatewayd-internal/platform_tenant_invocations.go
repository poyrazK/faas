package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func admitPlatformTenantInvocation(ctx context.Context, store state.Store, appID string, inv state.Invocation) (state.Invocation, error) {
	if store == nil {
		if _, err := uuid.Parse(inv.ID); err == nil && inv.Source == "esm" {
			return inv, fmt.Errorf("%w: durable queue invocation store unavailable", schedpkg.ErrPermanentInvoke)
		}
		if inv.PlatformTenantID == "" {
			return inv, nil
		}
		return inv, fmt.Errorf("%w: tenant invocation store unavailable", schedpkg.ErrPermanentInvoke)
	}
	admitted, err := state.AdmitPlatformTenantInvocation(ctx, store, appID, inv)
	if err != nil {
		return inv, fmt.Errorf("%w: tenant invocation admission: %w", schedpkg.ErrPermanentInvoke, err)
	}
	return admitted, nil
}
