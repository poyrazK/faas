package main

import (
	"context"
	"fmt"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func admitPlatformTenantInvocation(ctx context.Context, store state.Store, appID string, inv state.Invocation) (state.Invocation, error) {
	if store == nil {
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
