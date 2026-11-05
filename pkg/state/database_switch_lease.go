package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrDatabaseSwitchLeaseLost = errors.New("state: database switch execution lease lost")

type databaseSwitchLeaseKey struct{}

// WithDatabaseSwitchLease fences code preparation and publication by the switch
// lease. Native stores validate it under the traffic mutation's locks.
func WithDatabaseSwitchLease(ctx context.Context, sw DatabaseSwitch) context.Context {
	return context.WithValue(ctx, databaseSwitchLeaseKey{}, sw)
}
func databaseSwitchLease(ctx context.Context) (DatabaseSwitch, bool) {
	sw, ok := ctx.Value(databaseSwitchLeaseKey{}).(DatabaseSwitch)
	return sw, ok
}
func databaseSwitchLeaseTarget(sw DatabaseSwitch, d Deployment) bool {
	return sw.AppID == d.AppID && normalizedDeploymentScope(sw.Scope) == normalizedDeploymentScope(d.Scope) &&
		(d.ID == sw.TargetDeploymentID || d.ID == sw.FromDeploymentID)
}
func (m *MemStore) checkDatabaseSwitchLeaseLocked(ctx context.Context, d Deployment) error {
	expected, ok := databaseSwitchLease(ctx)
	if !ok {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, exists := m.databaseSwitches[expected.ID]
	if !exists || expected.LeaseToken == "" || current.LeaseToken != expected.LeaseToken || current.Phase != expected.Phase || DatabaseSwitchTerminal(current.Phase) ||
		current.LeaseUntil == nil || !current.LeaseUntil.After(time.Now().UTC()) || !databaseSwitchLeaseTarget(current, d) {
		return ErrDatabaseSwitchLeaseLost
	}
	return nil
}
func checkDatabaseSwitchLeaseTx(ctx context.Context, tx pgx.Tx, d Deployment) error {
	expected, ok := databaseSwitchLease(ctx)
	if !ok {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.LeaseToken == "" || !databaseSwitchLeaseTarget(expected, d) {
		return ErrDatabaseSwitchLeaseLost
	}
	_, err := sqlc.New().LockDatabaseSwitchExecution(ctx, tx, sqlc.LockDatabaseSwitchExecutionParams{
		ID: mustPgUUID(expected.ID), AppID: mustPgUUID(d.AppID), DeploymentID: mustPgUUID(d.ID), Scope: normalizedDeploymentScope(d.Scope), Token: expected.LeaseToken, Phase: expected.Phase,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDatabaseSwitchLeaseLost
	}
	return err
}
