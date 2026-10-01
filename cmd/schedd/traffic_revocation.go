// adr: 375
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func newInvocationTrafficRegistry(ctx context.Context, pool *pgxpool.Pool) (*trafficrevocation.Registry, error) {
	backend := state.NewPGTrafficSecurityBackend(pool)
	verifyCtx, cancel := context.WithTimeout(ctx, api.TrafficSecurityStoreTimeout)
	defer cancel()
	if _, err := backend.Read(verifyCtx, nil); err != nil {
		return nil, fmt.Errorf("schedd: verify invocation traffic security store before dispatch: %w", err)
	}
	return trafficrevocation.New(backend), nil
}

// Stop and join the repair worker before its pool is released on any exit.
func runInvocationTrafficRegistry(ctx context.Context, registry *trafficrevocation.Registry) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		registry.Run(ctx)
	}()
	return func() {
		cancel()
		registry.Close()
		<-done
	}
}
