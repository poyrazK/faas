// adr: 531
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func newPublicTrafficRevocationRegistry(ctx context.Context, pool *pgxpool.Pool) (*trafficrevocation.Registry, error) {
	backend := state.NewPGTrafficSecurityBackend(pool)
	verifyCtx, cancel := context.WithTimeout(ctx, api.TrafficSecurityStoreTimeout)
	defer cancel()
	if _, err := backend.Read(verifyCtx, nil); err != nil {
		return nil, fmt.Errorf("verify public traffic security store before serving: %w", err)
	}
	return trafficrevocation.New(backend), nil
}
