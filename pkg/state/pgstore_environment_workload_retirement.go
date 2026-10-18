package state

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsWorkloadRetirementStore = (*PgStore)(nil)
var _ EnvironmentWorkloadRetirementAdmissionStore = (*PgStore)(nil)

func workloadRetirementFromSQL(row sqlc.EnvironmentWorkloadRetirement) EnvironmentWorkloadRetirement {
	out := EnvironmentWorkloadRetirement{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID),
		EnvironmentID: pgUUIDString(row.EnvironmentID), SourceID: pgUUIDString(row.SourceID), Resource: row.Resource, AppID: pgUUIDString(row.AppID),
		RevisionID: pgUUIDString(row.RevisionID), Generation: row.Generation, PlanHash: row.PlanHash, Phase: row.Phase, JobID: pgUUIDString(row.JobID),
		RouteDeploymentID: pgUUIDString(row.RouteDeploymentID), RouteGeneration: row.RouteGeneration, ExpectedGateways: slices.Clone(row.ExpectedGateways),
		AcknowledgedGateways: slices.Clone(row.AcknowledgedGateways), RequestedAt: row.RequestedAt.Time, RecoveryRevisionID: pgUUIDString(row.RecoveryRevisionID), RecoveryGeneration: row.RecoveryGeneration}
	for _, id := range row.DeploymentIds {
		out.DeploymentIDs = append(out.DeploymentIDs, pgUUIDString(id))
	}
	for _, id := range row.QueueBindingIds {
		out.QueueBindingIDs = append(out.QueueBindingIDs, pgUUIDString(id))
	}
	if row.RetiredAt.Valid {
		t := row.RetiredAt.Time
		out.RetiredAt = &t
	}
	if row.RecoveredAt.Valid {
		t := row.RecoveredAt.Time
		out.RecoveredAt = &t
	}
	return out
}

func (s *PgStore) ListEnvironmentGitOpsWorkloadRetirements(ctx context.Context, accountID, sourceID string) ([]EnvironmentWorkloadRetirement, error) {
	rows, err := sqlc.New().ListEnvironmentWorkloadRetirements(ctx, s.pool, sqlc.ListEnvironmentWorkloadRetirementsParams{SourceID: mustPgUUID(sourceID), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentWorkloadRetirement, 0, len(rows))
	for _, row := range rows {
		out = append(out, workloadRetirementFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) EnvironmentWorkloadAdmissionRetired(ctx context.Context, appID, scope string) (bool, error) {
	blocked, err := sqlc.New().EnvironmentWorkloadAdmissionRetired(ctx, s.pool, sqlc.EnvironmentWorkloadAdmissionRetiredParams{AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope)})
	return blocked, mapErr(err)
}

// Reservation is its own transaction. The changed journal identity forces the
// worker to reread before preparing a graph; no execution is withdrawn here.
func (s *PgStore) BeginEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return nil, err
	}
	q := sqlc.New()
	if _, err = q.LockEnvironmentGitOpsIntentApps(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
		return nil, mapErr(err)
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(lease.Source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if lease.Source.Spec.Mode != "enforce" || !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	if _, err = q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, mapErr(err)
	}
	steps := []EnvironmentGitOpsStep{}
	for _, r := range snapshot.Retirements {
		_, wanted := desired.Definition.Workloads[strings.TrimPrefix(r.Resource, "workload/")]
		if r.Phase == "planned" && r.Generation != lease.Source.Generation {
			if _, err = q.CancelPlannedEnvironmentWorkloadRetirement(ctx, tx, sqlc.CancelPlannedEnvironmentWorkloadRetirementParams{ID: mustPgUUID(r.ID), SourceID: mustPgUUID(lease.Source.ID)}); err != nil {
				return nil, mapErr(err)
			}
			steps = append(steps, EnvironmentGitOpsStep{Resource: r.Resource, Path: "presence", Action: "cancel_retirement", Status: "applied"})
			continue
		}
		if !wanted && r.Phase == "recovering" && lease.Source.Generation > r.RecoveryGeneration {
			if _, err = q.AbandonEnvironmentWorkloadRecovery(ctx, tx, sqlc.AbandonEnvironmentWorkloadRecoveryParams{ID: mustPgUUID(r.ID), SourceID: mustPgUUID(lease.Source.ID)}); err != nil {
				return nil, mapErr(err)
			}
			steps = append(steps, EnvironmentGitOpsStep{Resource: r.Resource, Path: "presence", Action: "abandon_recovery", Status: "retired"})
			continue
		}
		if wanted && (r.Phase == "retired" || r.Phase == "recovering") && (r.RecoveryGeneration != lease.Source.Generation || r.RecoveryRevisionID != lease.Revision.ID) {
			if !workloadRetirementRecoveryMatches(desired.Definition.WorkloadRecoveries[strings.TrimPrefix(r.Resource, "workload/")], r.AppID) {
				return nil, ErrConflict
			}
			if _, err = q.BeginEnvironmentWorkloadRecovery(ctx, tx, sqlc.BeginEnvironmentWorkloadRecoveryParams{ID: mustPgUUID(r.ID), SourceID: mustPgUUID(lease.Source.ID), RevisionID: mustPgUUID(lease.Revision.ID), Generation: lease.Source.Generation}); err != nil {
				return nil, mapErr(err)
			}
			steps = append(steps, EnvironmentGitOpsStep{Resource: r.Resource, Path: "presence", Action: "recover", Status: "preparing"})
		}
	}
	if len(steps) == 0 {
		for _, resource := range workloadRetirementResources(plan) {
			raw, err := q.EnvironmentWorkloadRetirementInventory(ctx, tx, sqlc.EnvironmentWorkloadRetirementInventoryParams{SourceID: mustPgUUID(lease.Source.ID), Resource: resource})
			if err != nil {
				return nil, mapErr(err)
			}
			var inventory workloadRetirementInventory
			if json.Unmarshal(raw, &inventory) != nil {
				return nil, ErrConflict
			}
			if inventory.UnmanagedExecution || inventory.UnmanagedBinding {
				return nil, ErrConflict
			}
			params := sqlc.InsertEnvironmentWorkloadRetirementParams{SourceID: mustPgUUID(lease.Source.ID), AccountID: mustPgUUID(lease.Source.AccountID), ProjectID: mustPgUUID(lease.Source.ProjectID), EnvironmentID: mustPgUUID(lease.Source.EnvironmentID), Resource: resource, AppID: mustPgUUID(observed.State.ResourceIDs[resource]), RevisionID: mustPgUUID(lease.Revision.ID), Generation: lease.Source.Generation, PlanHash: plan.Hash, JobID: optionalRetirementUUID(inventory.JobID), DeploymentIds: []pgtype.UUID{}, QueueBindingIds: []pgtype.UUID{}}
			for _, id := range inventory.DeploymentIDs {
				params.DeploymentIds = append(params.DeploymentIds, mustPgUUID(id))
			}
			for _, id := range inventory.QueueBindingIDs {
				params.QueueBindingIds = append(params.QueueBindingIds, mustPgUUID(id))
			}
			params.RouteDeploymentID = optionalRetirementUUID(inventory.RouteDeploymentID)
			if _, err = q.InsertEnvironmentWorkloadRetirement(ctx, tx, params); err != nil {
				return nil, mapErr(err)
			}
			steps = append(steps, EnvironmentGitOpsStep{Resource: resource, Path: "presence", Action: "retire", Status: "planned"})
		}
	}
	if len(steps) > 0 {
		if err = q.TouchEnvironmentGitOpsIntent(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
			return nil, mapErr(err)
		}
	}
	return steps, mapErr(tx.Commit(ctx))
}

func optionalRetirementUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return mustPgUUID(id)
}

func retirementLeaseTx(ctx context.Context, tx pgx.Tx, lease EnvironmentGitOpsLease) error {
	_, err := sqlc.New().SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken)
	return mapErr(err)
}

func (s *PgStore) PendingEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease, nodes []string) ([]EnvironmentWorkloadRetirement, error) {
	nodes, err := canonicalGitOpsEffectNames(nodes)
	if err != nil {
		return nil, err
	}
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := retirementLeaseTx(ctx, tx, lease); err != nil {
		return nil, err
	}
	q := sqlc.New()
	roster, err := q.EnvironmentWorkloadRetirementGatewayRoster(ctx, tx)
	if err != nil {
		return nil, mapErr(err)
	}
	if !slices.Equal(nodes, roster) {
		return nil, ErrConflict
	}
	rows, err := q.ListEnvironmentWorkloadRetirements(ctx, tx, sqlc.ListEnvironmentWorkloadRetirementsParams{SourceID: mustPgUUID(lease.Source.ID), AccountID: mustPgUUID(lease.Source.AccountID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []EnvironmentWorkloadRetirement{}
	for _, row := range rows {
		if row.Phase != "retiring" {
			continue
		}
		r := workloadRetirementFromSQL(row)
		if r.RouteDeploymentID != "" && (r.RouteGeneration == 0 || !slices.Equal(r.ExpectedGateways, nodes)) {
			generation, err := q.NextDeploymentRouteGenerationTx(ctx, tx)
			if err != nil {
				return nil, mapErr(err)
			}
			row, err = q.PrepareEnvironmentWorkloadRetirementGateways(ctx, tx, sqlc.PrepareEnvironmentWorkloadRetirementGatewaysParams{ID: row.ID, SourceID: row.SourceID, RouteGeneration: generation, ExpectedGateways: nodes})
			if err != nil {
				return nil, mapErr(err)
			}
			r = workloadRetirementFromSQL(row)
		}
		out = append(out, r)
	}
	return out, mapErr(tx.Commit(ctx))
}
func (s *PgStore) AcknowledgeEnvironmentGitOpsWorkloadRetirement(ctx context.Context, lease EnvironmentGitOpsLease, id string, generation int64, node string) error {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := retirementLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	count, err := sqlc.New().AcknowledgeEnvironmentWorkloadRetirementGateway(ctx, tx, sqlc.AcknowledgeEnvironmentWorkloadRetirementGatewayParams{ID: mustPgUUID(id), SourceID: mustPgUUID(lease.Source.ID), RouteGeneration: generation, Node: node})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) ReconcileEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease) (bool, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := retirementLeaseTx(ctx, tx, lease); err != nil {
		return false, err
	}
	q := sqlc.New()
	rows, err := q.ListEnvironmentWorkloadRetirements(ctx, tx, sqlc.ListEnvironmentWorkloadRetirementsParams{SourceID: mustPgUUID(lease.Source.ID), AccountID: mustPgUUID(lease.Source.AccountID)})
	if err != nil {
		return false, mapErr(err)
	}
	ready := true
	for _, row := range rows {
		if row.Phase == "planned" || row.Phase == "recovering" {
			ready = false
			continue
		}
		if row.Phase != "retiring" {
			continue
		}
		if _, err = q.DrainEnvironmentWorkloadRetirementDeployments(ctx, tx, sqlc.DrainEnvironmentWorkloadRetirementDeploymentsParams{ID: row.ID, SourceID: row.SourceID}); err != nil {
			return false, mapErr(err)
		}
		drained, err := q.EnvironmentWorkloadRetirementDrained(ctx, tx, sqlc.EnvironmentWorkloadRetirementDrainedParams{ID: row.ID, SourceID: row.SourceID})
		if err != nil {
			return false, mapErr(err)
		}
		if !drained {
			ready = false
			continue
		}
		if _, err = q.CompleteEnvironmentWorkloadRetirement(ctx, tx, sqlc.CompleteEnvironmentWorkloadRetirementParams{ID: row.ID, SourceID: row.SourceID}); err != nil {
			return false, mapErr(err)
		}
	}
	return ready, mapErr(tx.Commit(ctx))
}

// Called only inside qualified release publication after candidate activation.
// Every external admission observes this fence in the same committed cutover.
func (s *PgStore) activateWorkloadRetirementsTx(ctx context.Context, tx pgx.Tx, lease EnvironmentGitOpsLease) error {
	q := sqlc.New()
	if err := retirementLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err := q.SetEnvironmentWorkloadRetirementCutoverContext(ctx, tx, lease.LeaseToken); err != nil {
		return mapErr(err)
	}
	rows, err := q.ListEnvironmentWorkloadRetirements(ctx, tx, sqlc.ListEnvironmentWorkloadRetirementsParams{SourceID: mustPgUUID(lease.Source.ID), AccountID: mustPgUUID(lease.Source.AccountID)})
	if err != nil {
		return mapErr(err)
	}
	for _, row := range rows {
		if row.Phase == "recovering" && row.RecoveryGeneration == lease.Source.Generation && pgUUIDString(row.RecoveryRevisionID) == lease.Revision.ID {
			count, err := q.ActivateEnvironmentWorkloadRecovery(ctx, tx, sqlc.ActivateEnvironmentWorkloadRecoveryParams{SourceID: row.SourceID, AppID: row.AppID, EnvironmentID: row.EnvironmentID, RevisionID: row.RecoveryRevisionID, Generation: row.RecoveryGeneration})
			if err != nil {
				return mapErr(err)
			}
			if count != 1 {
				return ErrConflict
			}
			continue
		}
		if row.Phase != "planned" {
			continue
		}
		if row.Generation != lease.Source.Generation || pgUUIDString(row.RevisionID) != lease.Revision.ID {
			return ErrConflict
		}
		raw, err := q.EnvironmentWorkloadRetirementInventory(ctx, tx, sqlc.EnvironmentWorkloadRetirementInventoryParams{SourceID: row.SourceID, Resource: row.Resource})
		if err != nil {
			return mapErr(err)
		}
		var inventory workloadRetirementInventory
		if json.Unmarshal(raw, &inventory) != nil || !workloadRetirementInventoryMatches(workloadRetirementFromSQL(row), inventory) {
			return ErrConflict
		}
		if _, err = q.StartEnvironmentWorkloadRetirement(ctx, tx, sqlc.StartEnvironmentWorkloadRetirementParams{ID: row.ID, SourceID: row.SourceID}); err != nil {
			return mapErr(err)
		}
		if err := q.WithdrawEnvironmentWorkloadRetirementTraffic(ctx, tx, sqlc.WithdrawEnvironmentWorkloadRetirementTrafficParams{ID: row.ID, SourceID: row.SourceID}); err != nil {
			return mapErr(err)
		}
		for _, id := range row.QueueBindingIds {
			if _, err = s.mutateQueueBindingConsumerTx(ctx, tx, lease.Source.AccountID, pgUUIDString(row.AppID), pgUUIDString(id), nil, nil, true); err != nil {
				return err
			}
		}
		if err = q.PauseEnvironmentWorkloadRetirementJob(ctx, tx, sqlc.PauseEnvironmentWorkloadRetirementJobParams{ID: row.ID, SourceID: row.SourceID}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func workloadReleaseExclusionsTx(ctx context.Context, tx sqlc.DBTX, source EnvironmentGitSource) (map[string]bool, error) {
	rows, err := sqlc.New().EnvironmentWorkloadRetirementReleaseExclusions(ctx, tx, sqlc.EnvironmentWorkloadRetirementReleaseExclusionsParams{AccountID: mustPgUUID(source.AccountID), ProjectID: mustPgUUID(source.ProjectID), Scope: normalizedDeploymentScope(source.EnvironmentSlug)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := map[string]bool{}
	for _, id := range rows {
		out[pgUUIDString(id)] = true
	}
	return out, nil
}

func (s *PgStore) ActivateEnvironmentGitOpsEmptyRetirement(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (bool, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockEnvironmentWorkloadRetirementProject(ctx, tx, sqlc.LockEnvironmentWorkloadRetirementProjectParams{ProjectID: mustPgUUID(lease.Source.ProjectID), AccountID: mustPgUUID(lease.Source.AccountID)}); err != nil {
		return false, mapErr(err)
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return false, err
	}
	if desired.Definition.Workloads == nil || len(desired.Definition.Workloads) != 0 {
		return false, ErrConflict
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	if err != nil {
		return false, err
	}
	plan, err := environmentGitOpsPlan(lease.Source, lease.Revision, desired, observed, false)
	if err != nil {
		return false, err
	}
	if !plan.CanApply() || plan.HasDrift() || plan.Hash != reviewed.Hash {
		return false, ErrConflict
	}
	needed := false
	for _, r := range snapshot.Retirements {
		if r.Phase == "planned" {
			needed = true
		}
	}
	if !needed {
		return true, nil
	}
	activeID, err := activeProjectReleaseSetIDTx(ctx, tx, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		return false, err
	}
	members, fallback, ttl, err := environmentGitOpsReleaseMembersTx(ctx, tx, lease.Source, EnvironmentWorkloadGraph{}, activeID)
	if err != nil {
		return false, err
	}
	required, err := q.EnvironmentBindingReleaseRequired(ctx, tx, sqlc.EnvironmentBindingReleaseRequiredParams{ProjectID: mustPgUUID(lease.Source.ProjectID), Scope: lease.Source.EnvironmentSlug})
	if err != nil {
		return false, mapErr(err)
	}
	if required && len(members) != 0 {
		return false, ErrBindingReleaseRequired
	}
	releaseID := uuid.NewString()
	if required {
		ctx = context.WithValue(ctx, qualifiedEnvironmentReleaseKey{}, releaseID)
	}
	before := func(ctx context.Context, tx pgx.Tx) error {
		if err := s.activateWorkloadRetirementsTx(ctx, tx, lease); err != nil {
			return err
		}
		if required {
			_, err := q.AuthorizeEnvironmentBindingRetirement(ctx, tx, sqlc.AuthorizeEnvironmentBindingRetirementParams{SourceID: mustPgUUID(lease.Source.ID), NextRelease: mustPgUUID(releaseID), PriorRelease: activeID, PlanHash: reviewed.Hash})
			return mapErr(err)
		}
		return nil
	}
	if _, err = publishProjectReleaseSetTx(ctx, tx, lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug, &activeID, fallback, ttl, members, true, before); err != nil {
		return false, err
	}
	return true, mapErr(tx.Commit(ctx))
}
