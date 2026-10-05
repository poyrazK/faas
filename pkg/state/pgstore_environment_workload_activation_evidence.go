package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsActivationEvidenceStore = (*PgStore)(nil)

func (s *PgStore) EnvironmentGitOpsActivationEvidence(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error) {
	var zero EnvironmentWorkloadActivationEvidence
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed); err != nil {
		return zero, err
	}
	q := sqlc.New()
	row, err := q.EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash})
	if err != nil {
		return zero, mapErr(err)
	}
	requests, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, row.ID)
	if err != nil {
		return zero, mapErr(err)
	}
	captures := map[string]EnvironmentQualificationSnapshotReceipt{}
	for _, request := range requests {
		if !request.ReservedInstanceID.Valid {
			continue
		}
		current, err := q.EnvironmentWorkloadQualificationInputsCurrent(ctx, tx, request.ID)
		if err != nil {
			return zero, mapErr(err)
		}
		if !current {
			continue
		}
		stored, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, request.ReservedInstanceID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return zero, mapErr(err)
		}
		receipt, err := qualificationSnapshotReceiptFromSQL(stored)
		if err != nil {
			return zero, err
		}
		captured := qualificationRequestFromSQL(request)
		fresh, err := readRuntimeConfigInputsFresh(ctx, tx, captured.AppID, receipt.Inputs)
		if err != nil {
			return zero, err
		}
		if fresh && receipt.Execution.RequestID == captured.ID && receipt.Execution.Attempt == captured.Attempt && receipt.Execution.Artifact == captured.Artifact {
			captures[captured.DeploymentID] = receipt
		}
	}
	return graphActivationEvidence(workloadGraphFromSQL(row), captures), nil
}
