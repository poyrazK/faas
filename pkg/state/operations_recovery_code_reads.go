// adr: 660
package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationRecoveryCodeAvailableTx(ctx context.Context, db sqlc.DBTX, op Operation) (bool, error) {
	q := sqlc.New()
	account, app, deployment := mustPgUUID(op.AccountID), mustPgUUID(op.AppID), mustPgUUID(op.DeploymentID)
	_, err := q.ReadCustomerOperationCodeApp(ctx, db, sqlc.ReadCustomerOperationCodeAppParams{AppID: app, AccountID: account})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, err := q.ReadCustomerOperationDeployment(ctx, db, sqlc.ReadCustomerOperationDeploymentParams{DeploymentID: deployment, AppID: app, AccountID: account, Scope: op.Scope})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil || status != string(DeployLive) {
		return false, err
	}
	if op.ReleaseID == "" {
		return true, nil
	}
	release := mustPgUUID(op.ReleaseID)
	bound := int32(api.ProjectReleaseSetMaxMembers + 1)
	count, err := q.CustomerOperationReleaseMemberCount(ctx, db, sqlc.CustomerOperationReleaseMemberCountParams{AccountID: account, AppID: app, DeploymentID: deployment, ReleaseID: release, Scope: op.Scope, MemberLimit: bound})
	if err != nil || count < 1 || count > api.ProjectReleaseSetMaxMembers {
		return false, err
	}
	apps, err := q.ReadCustomerOperationReleaseApps(ctx, db, sqlc.ReadCustomerOperationReleaseAppsParams{AccountID: account, ReleaseID: release, MemberLimit: bound})
	if err != nil || int64(len(apps)) != count {
		return false, err
	}
	deps, err := q.ReadCustomerOperationReleaseDeployments(ctx, db, sqlc.ReadCustomerOperationReleaseDeploymentsParams{AccountID: account, ReleaseID: release, MemberLimit: bound})
	if err != nil || int64(len(deps)) != count {
		return false, err
	}
	selected, err := q.ResolveRetainedProjectRelease(ctx, db, sqlc.ResolveRetainedProjectReleaseParams{AppID: app, ReleaseID: release, Scope: op.Scope})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && selected.DeploymentID == op.DeploymentID, err
}
