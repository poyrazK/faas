package db

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	ControlPlaneCellIDEnv     = "FAAS_CONTROL_PLANE_CELL_ID"
	ControlPlaneCellRegionEnv = "FAAS_CONTROL_PLANE_CELL_REGION"
)

// ErrControlPlaneCell marks a database identity/configuration rejection.
var ErrControlPlaneCell = errors.New("control plane cell identity rejected")

// CellIdentity is platform identity, independent of database addresses (ADR-601).
type CellIdentity struct {
	ID     string `json:"id"`
	Region string `json:"region"`
}

func (c CellIdentity) validate() error {
	id, err := uuid.Parse(c.ID)
	if err != nil || id == uuid.Nil || id.String() != c.ID || c.Region == "" || !api.ValidComputeRegion(c.Region) {
		return fmt.Errorf("%w: expected canonical nonzero UUID and compute region", ErrControlPlaneCell)
	}
	return nil
}

// installCellGuard runs on both ordinary and session pools. Unbound fleets
// pay only a connection-time check; configured cells check every checkout so
// a reused connection or transaction-pooler backend cannot bypass identity.
func installCellGuard(cfg *pgxpool.Config) error {
	expected := CellIdentity{ID: os.Getenv(ControlPlaneCellIDEnv), Region: os.Getenv(ControlPlaneCellRegionEnv)}
	configured := expected.ID != "" || expected.Region != ""
	if configured {
		if err := expected.validate(); err != nil {
			return err
		}
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return checkCellIdentity(ctx, conn, expected, configured)
	}
	if configured {
		cfg.PrepareConn = func(ctx context.Context, conn *pgx.Conn) (bool, error) {
			err := checkCellIdentity(ctx, conn, expected, true)
			return err == nil, err
		}
	}
	return nil
}

func checkCellIdentity(ctx context.Context, conn *pgx.Conn, expected CellIdentity, configured bool) error {
	queries := &sqlc.Queries{}
	if !configured {
		// Fresh legacy databases must be able to open before MigrateUp.
		ready, err := queries.ControlPlaneCellSchemaReady(ctx, conn)
		if err != nil {
			return fmt.Errorf("db: inspect cell schema: %w", err)
		}
		if !ready {
			return nil
		}
	}
	actual, err := queries.ReadControlPlaneCell(ctx, conn)
	if !configured && errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: db: read cell identity: %w", ErrControlPlaneCell, err)
	}
	if !configured {
		return fmt.Errorf("%w: bound database requires %s and %s", ErrControlPlaneCell, ControlPlaneCellIDEnv, ControlPlaneCellRegionEnv)
	}
	if actual.CellID != expected.ID || actual.ComputeRegion != expected.Region {
		return fmt.Errorf("%w: expected %s/%s, database is %s/%s", ErrControlPlaneCell,
			expected.ID, expected.Region, actual.CellID, actual.ComputeRegion)
	}
	return nil
}
