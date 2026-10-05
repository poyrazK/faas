package main

// adr: 632
import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func loadRegionalRuntimeRetirement(ctx context.Context, cfg Config, pool *pgxpool.Pool, store state.RegionalRuntimeCapacityLedger, router *sched.VMMRouter, authenticated bool) (*sched.RegionalRuntimeRetirement, error) {
	if !cfg.RegionalRuntimeSigning.Configured() {
		return nil, nil
	}
	if !authenticated {
		return nil, fmt.Errorf("schedd: regional retirement requires node-verified vmmd mTLS: %w", runtimequota.ErrTrust)
	}
	cell, err := (&sqlc.Queries{}).ReadControlPlaneCell(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("schedd: read immutable regional cell identity: %w", err)
	}
	return regionalRetirementForCell(cfg, db.CellIdentity{ID: cell.CellID, Region: cell.ComputeRegion}, store, router)
}

func regionalRetirementForCell(cfg Config, cell db.CellIdentity, store state.RegionalRuntimeCapacityLedger, router *sched.VMMRouter) (*sched.RegionalRuntimeRetirement, error) {
	if cell.Region == "" || !api.ValidComputeRegion(cell.Region) {
		return nil, runtimequota.ErrBinding
	}
	keys, err := runtimequota.LoadAuthorityKeys(cfg.RegionalRuntimeSigning, runtimequota.Cell, cell.ID)
	if err != nil {
		return nil, fmt.Errorf("schedd: load regional cell signing material: %w", err)
	}
	return sched.NewRegionalRuntimeRetirement(store, keys, router)
}
