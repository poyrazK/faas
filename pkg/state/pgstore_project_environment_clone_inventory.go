package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// requireCloneSourceInventoryTx runs in the capture transaction, so the
// inventory and the captured configuration come from one snapshot. It fails
// before an operation exists when the source holds rows in any table the
// clone cannot copy into an isolated target.
func requireCloneSourceInventoryTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		return err
	}
	probes, err := cloneInventoryProbes(policies)
	if err != nil {
		return err
	}
	q := new(sqlc.Queries)
	environmentID, err := q.ReadProjectEnvironmentCloneSourceEnvironmentID(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceEnvironmentIDParams{
		ProjectID: mustPgUUID(op.ProjectID), Slug: op.SourceEnvironment})
	if err != nil {
		return mapErr(err)
	}
	apps, deployments := make([]pgtype.UUID, 0, len(records)), make([]pgtype.UUID, 0, len(records))
	for _, record := range records {
		apps = append(apps, mustPgUUID(record.AppID))
		if record.SourceDeploymentID != "" {
			deployments = append(deployments, mustPgUUID(record.SourceDeploymentID))
		}
	}
	slugs := []string{op.SourceEnvironment}
	if IsProductionDeploymentScope(op.SourceEnvironment) {
		slugs = append(slugs, DefaultEnvScope)
	}
	tables, err := q.ReadProjectEnvironmentCloneInventoryPresent(ctx, tx, sqlc.ReadProjectEnvironmentCloneInventoryPresentParams{
		ProjectID: mustPgUUID(op.ProjectID), EnvironmentID: environmentID, EnvironmentSlugs: slugs, AppIds: apps, DeploymentIds: deployments, Probes: probes})
	if err != nil {
		return mapErr(err)
	}
	present := make(map[string]bool, len(tables))
	for _, table := range tables {
		present[table] = true
	}
	if blockers := cloneInventoryBlockers(policies, present); len(blockers) > 0 {
		return &ProjectEnvironmentCloneInventoryError{Blockers: blockers}
	}
	return nil
}
