package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DrainPreview counts a fenced app's instances by how a drain plan would
// treat them (ADR-20261023181931): Supported members retire with a native
// receipt, Parked ones have no process, and Blocking ones would stop the
// plan. Overflow means the app has more instances than one plan captures.
type DrainPreview struct {
	Supported, Parked, Blocking int
	Overflow                    bool
}

// Settled reports whether a drain plan captured now could complete.
func (p DrainPreview) Settled() bool { return p.Blocking == 0 && !p.Overflow }

// DatabaseSwitchDrainStore reads the drain evidence a database switch waits
// on. Only the PostgreSQL store holds the scheduler's drain plans.
type DatabaseSwitchDrainStore interface {
	PreviewDrainBlockers(ctx context.Context, appID string) (DrainPreview, error)
	// DrainPlanForCutover returns the plan captured under the given fence,
	// and false when schedd has not captured one yet.
	DrainPlanForCutover(ctx context.Context, cutoverID, appID string, fencedAt time.Time) (ManagedPostgresDrainPlan, bool, error)
}

var _ DatabaseSwitchDrainStore = (*PgStore)(nil)

func (s *PgStore) PreviewDrainBlockers(ctx context.Context, appID string) (DrainPreview, error) {
	if !qualificationRecoveryUUIDValid(appID) {
		return DrainPreview{}, ErrInvalidArgument
	}
	rows, err := sqlc.New().PreviewManagedPostgresDrainInventory(ctx, s.pool, sqlc.PreviewManagedPostgresDrainInventoryParams{
		AppID: appID, PageLimit: api.ManagedPostgresCutoverDrainPlanMembersMax + 1})
	if err != nil {
		return DrainPreview{}, mapErr(err)
	}
	var p DrainPreview
	if len(rows) > api.ManagedPostgresCutoverDrainPlanMembersMax {
		p.Overflow = true
	}
	for _, row := range rows {
		switch drainPlanInventoryBlocker(sqlc.ListManagedPostgresDrainPlanInventoryRow(row)) {
		case "":
			p.Supported++
		case "parked_instance_unproven":
			p.Parked++
		default:
			p.Blocking++
		}
	}
	return p, nil
}

func (s *PgStore) DrainPlanForCutover(ctx context.Context, cutoverID, appID string, fencedAt time.Time) (ManagedPostgresDrainPlan, bool, error) {
	if !qualificationRecoveryUUIDValid(cutoverID) || !qualificationRecoveryUUIDValid(appID) || fencedAt.IsZero() {
		return ManagedPostgresDrainPlan{}, false, ErrInvalidArgument
	}
	id, err := sqlc.New().ManagedPostgresCutoverDrainPlanForFence(ctx, s.pool, sqlc.ManagedPostgresCutoverDrainPlanForFenceParams{
		CutoverID: cutoverID, AppID: appID, FencedAt: pgTimestamptzUTC(fencedAt)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedPostgresDrainPlan{}, false, nil
	}
	if err != nil {
		return ManagedPostgresDrainPlan{}, false, mapErr(err)
	}
	plan, err := s.ManagedPostgresDrainPlan(ctx, id)
	return plan, err == nil, err
}
