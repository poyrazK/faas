package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func registerRuntimeAdmissionIdentity(ctx context.Context, store state.Store, identity runtimeadmission.Identity) error {
	registrar, ok := store.(state.ComputeNodeRuntimeIdentityStore)
	if !ok {
		return fmt.Errorf("vmmd: native process registrar unavailable")
	}
	if err := registrar.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		return fmt.Errorf("vmmd: register native process: %w", err)
	}
	return nil
}
