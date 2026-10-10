package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsQualificationStore = (*PgStore)(nil)

func qualificationRequestFromSQL(row sqlc.EnvironmentWorkloadQualificationRequest) EnvironmentWorkloadQualificationRequest {
	request := EnvironmentWorkloadQualificationRequest{ID: pgUUIDString(row.ID), GraphID: pgUUIDString(row.GraphID), DeploymentID: pgUUIDString(row.DeploymentID),
		AppID: pgUUIDString(row.AppID), Resource: row.Resource, ExecutionMode: row.ExecutionMode, Phase: row.Phase, CreatedAt: row.CreatedAt.Time,
		WorkerID: row.WorkerID, LeaseToken: row.LeaseToken, Attempt: row.Attempt, ReservedInstanceID: pgUUIDString(row.ReservedInstanceID)}
	_ = json.Unmarshal(row.Artifact, &request.Artifact)
	_ = json.Unmarshal(row.FrozenInputs, &request.FrozenInputs)
	if row.LeaseUntil.Valid {
		until := row.LeaseUntil.Time
		request.LeaseUntil = &until
	}
	return request
}

func (s *PgStore) QueueEnvironmentGitOpsQualification(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadQualificationRequest, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed); err != nil {
		return nil, err
	}
	row, err := q.EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{
		SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash})
	if err != nil {
		return nil, mapErr(err)
	}
	if row.Phase != "prepared" {
		return nil, ErrConflict
	}
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, mapErr(err)
	}
	graph := workloadGraphFromSQL(row)
	fresh := map[string]bool{}
	expected := 0
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		expected++
		count, err := q.CreateEnvironmentWorkloadQualification(ctx, tx, sqlc.CreateEnvironmentWorkloadQualificationParams{
			GraphID: row.ID, DeploymentID: mustPgUUID(member.CandidateDeploymentID), Resource: member.Resource})
		if err != nil {
			return nil, mapErr(err)
		}
		fresh[member.Resource] = count == 1
	}
	rows, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, row.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(rows) != expected {
		return nil, ErrConflict
	}
	if len(rows) > 0 {
		current, err := q.EnvironmentWorkloadQualificationArtifactCurrent(ctx, tx, rows[0].ID)
		if err != nil {
			return nil, mapErr(err)
		}
		if !current {
			return nil, ErrConflict
		}
	}
	requests := make([]EnvironmentWorkloadQualificationRequest, 0, len(rows))
	for _, row := range rows {
		request := qualificationRequestFromSQL(row)
		requests = append(requests, request)
		if fresh[request.Resource] {
			payload, _ := json.Marshal(map[string]string{"qualification_id": request.ID, "graph_id": graph.ID, "app_id": request.AppID, "deployment_id": request.DeploymentID})
			if err := db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyEnvironmentWorkloadQualify, string(payload)); err != nil {
				return nil, mapErr(err)
			}
		}
	}
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	return requests, mapErr(tx.Commit(ctx))
}

// Qualification is execution work authorized by the durable apid request. It
// does not borrow the controller's token. Re-read the full reviewed observation
// under source/app locks so inherited settings and original identities remain
// part of the fence, even when they do not increment an owned intent field.
func (s *PgStore) qualificationCurrentTx(ctx context.Context, tx pgx.Tx, id string) (sqlc.EnvironmentWorkloadQualificationRequest, error) {
	q := sqlc.New()
	sourceRow, err := q.EnvironmentWorkloadQualificationSourceForUpdate(ctx, tx, mustPgUUID(id))
	if err != nil {
		return sqlc.EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	if _, err := q.LockEnvironmentGitOpsCandidateApps(ctx, tx, sourceRow.ID); err != nil {
		return sqlc.EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	row, err := q.EnvironmentWorkloadQualificationForUpdate(ctx, tx, mustPgUUID(id))
	if err != nil {
		return row, mapErr(err)
	}
	graph, err := q.EnvironmentWorkloadGraphByIDForUpdate(ctx, tx, row.GraphID)
	if err != nil {
		return row, mapErr(err)
	}
	if graph.Phase != "prepared" || sourceRow.Mode != "enforce" || sourceRow.Suspended || graph.Generation != sourceRow.Generation ||
		graph.IntentVersion != sourceRow.IntentVersion || graph.RevisionID != sourceRow.ApprovedRevisionID || graph.EnvironmentID != sourceRow.EnvironmentID {
		return row, ErrConflict
	}
	source := environmentGitSourceFromSQL(sourceRow, "")
	if _, _, _, err := s.environmentCandidateInputsTx(ctx, tx, EnvironmentGitOpsLease{Source: source}, environmentsync.Plan{Hash: graph.PlanHash}); err != nil {
		return row, err
	}
	current, err := q.EnvironmentWorkloadQualificationInputsCurrent(ctx, tx, row.ID)
	if err != nil {
		return row, mapErr(err)
	}
	if !current {
		return row, ErrConflict
	}
	return row, nil
}

func (s *PgStore) ClaimEnvironmentWorkloadQualification(ctx context.Context, id, workerID string, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	return s.claimEnvironmentWorkloadQualification(ctx, id, "", workerID, duration)
}

func (s *PgStore) ClaimEnvironmentWorkloadQualificationForNode(ctx context.Context, id, nodeID, workerID string, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationRecoveryUUIDValid(nodeID) {
		return EnvironmentWorkloadQualificationRequest{}, ErrInvalidArgument
	}
	return s.claimEnvironmentWorkloadQualification(ctx, id, nodeID, workerID, duration)
}

func (s *PgStore) claimEnvironmentWorkloadQualification(ctx context.Context, id, nodeID, workerID string, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(id, workerID, duration) {
		return EnvironmentWorkloadQualificationRequest{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := s.qualificationCurrentTx(ctx, tx, id)
	if err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	claimedRequest := qualificationRequestFromSQL(current)
	if claimedRequest.ExecutionMode == api.ExecutionModeJob || len(claimedRequest.FrozenInputs.ServiceBindings) != 0 {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	q, token := sqlc.New(), uuid.NewString()
	if current.Attempt > 0 {
		if _, err := q.EnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationSmokeReceiptParams{
			RequestID: current.ID, Attempt: current.Attempt,
		}); err == nil {
			return EnvironmentWorkloadQualificationRequest{}, ErrConflict
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
		}
	}
	if nodeID != "" {
		app, err := q.EnvironmentWorkloadQualificationAppOwner(ctx, tx, current.AppID)
		if err != nil {
			return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
		}
		if current.ExecutionMode == "job" || current.ExecutionMode == "worker" || (app.Status != string(AppActive) && app.Status != string(AppEvictedCold)) ||
			(app.NodeID.Valid && app.NodeID != mustPgUUID(nodeID)) {
			return EnvironmentWorkloadQualificationRequest{}, ErrConflict
		}
	}
	instanceID := ""
	if current.ExecutionMode != "job" {
		instanceID = uuid.NewString()
	}
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, token); err != nil {
		return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	row, err := q.ClaimEnvironmentWorkloadQualification(ctx, tx, sqlc.ClaimEnvironmentWorkloadQualificationParams{
		ID: mustPgUUID(id), WorkerID: workerID, Token: token, DurationUs: duration.Microseconds(), InstanceID: mustPgUUID(instanceID)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentWorkloadQualificationRequest{}, ErrConflict
		}
		return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	return qualificationRequestFromSQL(row), mapErr(tx.Commit(ctx))
}

// Claim an entire service-binding cohort under one transaction. If any member
// is stale, owned by another node, unsupported or still has an active attempt,
// no sibling lease or instance identity is issued.
func (s *PgStore) ClaimEnvironmentWorkloadQualificationGraphForNode(ctx context.Context, graphID, nodeID, workerID string, duration time.Duration) ([]EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(graphID, workerID, duration) || !qualificationRecoveryUUIDValid(nodeID) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	graphUUID := mustPgUUID(graphID)
	rows, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, graphUUID)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(rows) == 0 {
		return nil, ErrConflict
	}
	if _, err := s.qualificationCurrentTx(ctx, tx, pgUUIDString(rows[0].ID)); err != nil {
		return nil, err
	}
	graphRow, err := q.EnvironmentWorkloadGraphByIDForUpdate(ctx, tx, graphUUID)
	if err != nil {
		return nil, mapErr(err)
	}
	graph := workloadGraphFromSQL(graphRow)
	expected, completeSmokeReceipts := 0, true
	for _, member := range graph.Members {
		if member.CandidateDeploymentID != "" {
			expected++
		}
	}
	if expected == 0 || len(rows) != expected {
		return nil, ErrConflict
	}
	for _, row := range rows {
		current, err := s.qualificationCurrentTx(ctx, tx, pgUUIDString(row.ID))
		if err != nil {
			return nil, err
		}
		request := qualificationRequestFromSQL(current)
		if request.Phase != "queued" && (request.Phase != "claimed" || request.LeaseUntil == nil || time.Now().Before(*request.LeaseUntil)) {
			return nil, ErrConflict
		}
		if !qualificationGraphSmokePolicyValid(request) {
			return nil, ErrConflict
		}
		if request.ExecutionMode == api.ExecutionModeJob && !qualificationGraphJobQueueBindingsSupported(graph, request.Resource) {
			return nil, ErrConflict
		}
		if request.ExecutionMode == api.ExecutionModeJob {
			if _, err := q.EnvironmentQualificationJobSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationJobSmokeReceiptParams{
				RequestID: current.ID, Attempt: current.Attempt,
			}); errors.Is(err, pgx.ErrNoRows) {
				completeSmokeReceipts = false
			} else if err != nil {
				return nil, mapErr(err)
			}
		} else {
			if _, err := q.EnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationSmokeReceiptParams{
				RequestID: current.ID, Attempt: current.Attempt,
			}); errors.Is(err, pgx.ErrNoRows) {
				completeSmokeReceipts = false
			} else if err != nil {
				return nil, mapErr(err)
			}
		}
		app, err := q.EnvironmentWorkloadQualificationAppOwner(ctx, tx, current.AppID)
		if err != nil {
			return nil, mapErr(err)
		}
		if (app.Status != string(AppActive) && app.Status != string(AppEvictedCold)) ||
			(app.NodeID.Valid && app.NodeID != mustPgUUID(nodeID)) || (app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1) {
			return nil, ErrConflict
		}
	}
	if completeSmokeReceipts {
		return nil, ErrConflict
	}
	claimed := make([]EnvironmentWorkloadQualificationRequest, 0, len(rows))
	for _, row := range rows {
		token, instanceID := uuid.NewString(), uuid.NewString()
		if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, token); err != nil {
			return nil, mapErr(err)
		}
		updated, err := q.ClaimEnvironmentWorkloadQualification(ctx, tx, sqlc.ClaimEnvironmentWorkloadQualificationParams{
			ID: row.ID, WorkerID: workerID, Token: token, DurationUs: duration.Microseconds(), InstanceID: mustPgUUID(instanceID)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrConflict
			}
			return nil, mapErr(err)
		}
		claimed = append(claimed, qualificationRequestFromSQL(updated))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return claimed, nil
}

func (s *PgStore) ValidateEnvironmentWorkloadQualification(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest) error {
	if _, err := uuid.Parse(claimed.ID); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return err
	}
	if !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) RenewEnvironmentWorkloadQualification(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(claimed.ID, claimed.WorkerID, duration) {
		return EnvironmentWorkloadQualificationRequest{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	if !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	q := sqlc.New()
	if _, err := q.SetEnvironmentWorkloadQualificationContext(ctx, tx, claimed.LeaseToken); err != nil {
		return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	row, err = q.RenewEnvironmentWorkloadQualification(ctx, tx, sqlc.RenewEnvironmentWorkloadQualificationParams{
		ID: row.ID, Token: claimed.LeaseToken, Attempt: claimed.Attempt, DurationUs: duration.Microseconds()})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnvironmentWorkloadQualificationRequest{}, ErrConflict
		}
		return EnvironmentWorkloadQualificationRequest{}, mapErr(err)
	}
	return qualificationRequestFromSQL(row), mapErr(tx.Commit(ctx))
}
