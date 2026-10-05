package state

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsGraphPreparationStore = (*PgStore)(nil)

func workloadGraphFromSQL(row sqlc.EnvironmentWorkloadGraph) EnvironmentWorkloadGraph {
	graph := EnvironmentWorkloadGraph{ID: pgUUIDString(row.ID), SourceID: pgUUIDString(row.SourceID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		RevisionID: pgUUIDString(row.RevisionID), Generation: row.Generation, IntentVersion: row.IntentVersion, PlanHash: row.PlanHash,
		DefinitionDigest: row.DefinitionDigest, Phase: row.Phase, ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt.Time}
	_ = json.Unmarshal(row.Members, &graph.Members)
	_ = json.Unmarshal(row.ResourceIds, &graph.ResourceIDs)
	if row.PreparedAt.Valid {
		at := row.PreparedAt.Time
		graph.PreparedAt = &at
	}
	return graph
}

func (s *PgStore) ReconcileEnvironmentGitOpsPreparation(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadGraph, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	inputs, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed)
	if err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	row, err := q.EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{
		SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash})
	if err != nil {
		return EnvironmentWorkloadGraph{}, mapErr(err)
	}
	candidates := []EnvironmentWorkloadCandidate{}
	for _, input := range inputs {
		frozen := candidateFrozenInputs(input)
		id, err := q.EnvironmentGitOpsCandidateByInput(ctx, tx, sqlc.EnvironmentGitOpsCandidateByInputParams{
			SourceID: frozen.SourceID, Generation: strconv.FormatInt(frozen.Generation, 10), Resource: frozen.Resource, PlanHash: frozen.PlanHash})
		if err != nil {
			return EnvironmentWorkloadGraph{}, mapErr(err)
		}
		dep, err := q.EnvironmentGitOpsImageCandidate(ctx, tx, id)
		if err != nil {
			return EnvironmentWorkloadGraph{}, mapErr(err)
		}
		candidates = append(candidates, EnvironmentWorkloadCandidate{Status: DeploymentStatus(dep.Status), HasRootfs: dep.RootfsPath != "" || dep.RootfsKey != ""})
	}
	phase, code := preparationGraphPhase(candidates)
	if row.Phase != "failed" && row.Phase != phase {
		if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
			return EnvironmentWorkloadGraph{}, mapErr(err)
		}
		row, err = q.AdvanceEnvironmentWorkloadGraphPreparation(ctx, tx, sqlc.AdvanceEnvironmentWorkloadGraphPreparationParams{
			ID: row.ID, Phase: phase, ErrorCode: code})
		if err != nil {
			return EnvironmentWorkloadGraph{}, mapErr(err)
		}
	}
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	return workloadGraphFromSQL(row), mapErr(tx.Commit(ctx))
}
