// adr: 375
package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) beginDeploymentTrafficMutation(ctx context.Context, id string) (pgx.Tx, error) {
	account, err := sqlc.New().ReadDeploymentTrafficAccount(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return nil, mapErr(err)
	}
	return s.beginTrafficPolicyMutation(ctx, account)
}

func (s *PgStore) updateTrafficDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errMsg string) error {
	tx, err := s.beginDeploymentTrafficMutation(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New()
	changed, err := queries.UpdateTrafficDeploymentStatus(ctx, tx, sqlc.UpdateTrafficDeploymentStatusParams{
		DeploymentID: mustPgUUID(id), Status: string(status), Error: pgtype.Text{String: errMsg, Valid: errMsg != ""},
	})
	if err != nil {
		return mapErr(err)
	}
	if changed == 0 {
		if _, err := queries.ReadTrafficDeploymentStatus(ctx, tx, mustPgUUID(id)); err != nil {
			return mapErr(err)
		}
		return ErrInvalidStateTransition
	}
	return tx.Commit(ctx)
}

// Only the target's eligibility changes here. Other alias inputs and ordinary
// host policy remain unchanged; the caller holds m.mu and publishes afterward.
func (m *MemStore) checkMemTrafficDeploymentChangeLocked(ctx context.Context, after Deployment) error {
	if !after.DeploymentAliasActive() {
		return nil
	}
	app, found := m.apps[after.AppID]
	if !found {
		return ErrNotFound
	}
	view, err := m.readBoundedMemTrafficAnalysisLocked(ctx, app.AccountID)
	if err != nil {
		return err
	}
	return m.checkMemTrafficPolicyChangeLocked(ctx, app.AccountID, view, memTrafficPolicyChange{Deployments: map[string]Deployment{after.ID: after}})
}
