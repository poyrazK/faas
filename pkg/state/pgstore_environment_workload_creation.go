package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsWorkloadCreationStore = (*PgStore)(nil)

func (s *PgStore) PrepareEnvironmentGitOpsWorkloads(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitOpsIntentApps(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.QueueConsumerLockAccount(ctx, tx, mustPgUUID(lease.Source.AccountID)); err != nil {
		return nil, mapErr(err)
	}
	source, revision, desired, err := lockApprovedEnvironmentGitOps(ctx, tx, lease.Source.AccountID, lease.Source.ID)
	if err != nil {
		return nil, err
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(source, revision, desired, observed, false)
	if err != nil || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	apps, err := missingEnvironmentWorkloads(source, desired, snapshot, plan)
	if err != nil || len(apps) == 0 {
		return nil, err
	}
	limits, _ := api.LimitsFor(snapshot.Plan)
	org, err := q.OrgByPersonalAccount(ctx, tx, mustPgUUID(source.AccountID))
	if err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, err
	}
	for _, resource := range environmentWorkloadCreationNames(apps) {
		app := apps[resource]
		app.OrgID = pgUUIDString(org.ID)
		app, err = createAppIfUnderQuotaTx(ctx, tx, app, limits)
		if err != nil {
			return nil, err
		}
		apps[resource], observed.State.ResourceIDs[resource] = app, app.ID
		count, err := q.BindEnvironmentGitOpsResource(ctx, tx, sqlc.BindEnvironmentGitOpsResourceParams{SourceID: mustPgUUID(source.ID), Resource: resource, AppID: mustPgUUID(app.ID)})
		if err != nil {
			return nil, mapErr(err)
		}
		if count != 1 {
			return nil, ErrConflict
		}
	}
	createdPlan := plan
	createdPlan.Changes = environmentWorkloadCreationChanges(plan, apps)
	for _, row := range changedWorkloadIntents(snapshot, createdPlan, observed.State.ResourceIDs, false) {
		row.AccountID = source.AccountID
		if _, err := putWorkloadIntentTx(ctx, tx, row); err != nil {
			return nil, err
		}
	}
	steps := make([]EnvironmentGitOpsStep, 0, len(createdPlan.Changes))
	for _, change := range createdPlan.Changes {
		count, err := q.OwnEnvironmentGitOpsField(ctx, tx, sqlc.OwnEnvironmentGitOpsFieldParams{SourceID: mustPgUUID(source.ID), Resource: change.Resource, FieldPath: change.Path, Value: change.After})
		if err != nil {
			return nil, mapErr(err)
		}
		if count != 1 {
			return nil, ErrConflict
		}
		steps = append(steps, EnvironmentGitOpsStep{Resource: change.Resource, Path: change.Path, Action: "create", Status: "applied"})
	}
	if err := q.TouchEnvironmentGitOpsIntent(ctx, tx, mustPgUUID(source.ID)); err != nil {
		return nil, err
	}
	rawPlan, _ := json.Marshal(plan)
	rawSteps, _ := json.Marshal(steps)
	count, err := q.SaveEnvironmentGitOpsProgress(ctx, tx, sqlc.SaveEnvironmentGitOpsProgressParams{RunID: mustPgUUID(lease.RunID), SourceID: mustPgUUID(source.ID), LeaseToken: lease.LeaseToken, Plan: rawPlan, Steps: rawSteps})
	if err != nil {
		return nil, mapErr(err)
	}
	if count != 1 {
		return nil, ErrConflict
	}
	if err := recordGitOpsEvent(ctx, tx, source.ID, source.AccountID, "control", map[string]any{"operation": "workload_prepare", "plan_hash": plan.Hash, "workloads": len(apps)}); err != nil {
		return nil, err
	}
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return steps, nil
}
