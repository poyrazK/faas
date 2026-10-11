package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeVerificationJournalStore = (*PgStore)(nil)

func (s *PgStore) StartRuntimeUpgradeVerification(ctx context.Context, accountID, id string, sessions []string) (RuntimeUpgradeVerificationJournal, error) {
	out, err := newRuntimeUpgradeVerification(id, sessions, time.Now().UTC())
	if err != nil || validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerificationJournal{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("begin verification enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.LockRuntimeUpgradeOperationControl(ctx, tx, sqlc.LockRuntimeUpgradeOperationControlParams{ID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, mapErr(err)
	}
	old, err := q.GetRuntimeUpgradeVerification(ctx, tx, sqlc.GetRuntimeUpgradeVerificationParams{OperationID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if err == nil {
		if !slices.Equal(out.GatewaySessions, runtimeUpgradeVerificationSessions(old.GatewaySessions)) {
			return RuntimeUpgradeVerificationJournal{}, ErrConflict
		}
		return runtimeUpgradeVerificationJournalFromRow(old)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("read verification enrollment: %w", err)
	}
	op := runtimeUpgradeOperationFromRow(r)
	if op.Phase != RuntimeUpgradeComplete {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	c, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(op.DeploymentID))
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("read verification activation: %w", mapErr(err))
	}
	if !op.cutover(op.WakeID).matches(runtimeUpgradeCutoverFromRow(c)) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	if _, err := q.ShareRuntimeUpgradeGatewayRosterHead(ctx, tx); err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("fence verification gateway roster: %w", err)
	}
	roster, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("read enrollment gateway roster: %w", err)
	}
	if !slices.Equal(out.GatewaySessions, runtimeUpgradeGatewayRosterFromRow(roster).sessions()) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	participants := make([]pgtype.UUID, len(out.GatewaySessions))
	for i, session := range out.GatewaySessions {
		participants[i] = mustPgUUID(session)
	}
	row, err := q.InsertRuntimeUpgradeVerification(ctx, tx, sqlc.InsertRuntimeUpgradeVerificationParams{OperationID: mustPgUUID(id), GatewaySessions: participants, GatewayRosterRevision: roster.Revision, CutoverAt: c.CutoverAt, DeadlineSeconds: int32(api.RuntimeUpgradeVerificationMaxAge / time.Second)})
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("freeze verification participants: %w", runtimeUpgradeBaselineError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("commit verification enrollment: %w", err)
	}
	return runtimeUpgradeVerificationJournalFromRow(row)
}

func (s *PgStore) RuntimeUpgradeVerificationJournal(ctx context.Context, accountID, id string) (RuntimeUpgradeVerificationJournal, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerificationJournal{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetRuntimeUpgradeVerification(ctx, s.pool, sqlc.GetRuntimeUpgradeVerificationParams{OperationID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("read verification journal: %w", mapErr(err))
	}
	return runtimeUpgradeVerificationJournalFromRow(row)
}

func (s *PgStore) ClaimRuntimeUpgradeVerification(ctx context.Context) (RuntimeUpgradeOperationClaim, error) {
	row, err := sqlc.New().ClaimRuntimeUpgradeVerification(ctx, s.pool, int32(api.RuntimeUpgradeOperationLease/time.Second))
	if err != nil {
		return RuntimeUpgradeOperationClaim{}, mapErr(err)
	}
	return RuntimeUpgradeOperationClaim{ID: pgUUIDString(row.OperationID), LeaseToken: pgUUIDString(row.LeaseToken)}, nil
}

func (s *PgStore) AdvanceRuntimeUpgradeVerification(ctx context.Context, claim RuntimeUpgradeOperationClaim) (RuntimeUpgradeVerificationJournal, error) {
	if err := claim.validate(); err != nil {
		return RuntimeUpgradeVerificationJournal{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("begin verification advance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.GetRuntimeUpgradeOperation(ctx, tx, mustPgUUID(claim.ID))
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, mapErr(err)
	}
	// Operation -> journal -> roster -> environment/app/workload fence order.
	r, err = q.LockRuntimeUpgradeOperationControl(ctx, tx, sqlc.LockRuntimeUpgradeOperationControlParams{ID: r.ID, AccountID: r.AccountID})
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("lock verification operation: %w", err)
	}
	row, err := q.LockRuntimeUpgradeVerification(ctx, tx, sqlc.LockRuntimeUpgradeVerificationParams{OperationID: mustPgUUID(claim.ID), LeaseToken: mustPgUUID(claim.LeaseToken)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("lock verification lease: %w", err)
	}
	j, err := runtimeUpgradeVerificationJournalFromRow(row)
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, err
	}
	// Roster review/heartbeat take only this head fence; no app lock inversion.
	if _, err := q.ShareRuntimeUpgradeGatewayRosterHead(ctx, tx); err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("fence verification membership: %w", err)
	}
	op := runtimeUpgradeOperationFromRow(r)
	observation, err := advanceRuntimeUpgradeVerificationDB(ctx, tx, op, j)
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("evaluate durable verification: %w", err)
	}
	j = runtimeUpgradeVerificationCheckpoint(j, observation, time.Now().UTC())
	evidence, err := json.Marshal(observation)
	if err != nil || len(evidence) > api.RuntimeUpgradeVerificationMaxBytes {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	row, err = q.CheckpointRuntimeUpgradeVerification(ctx, tx, sqlc.CheckpointRuntimeUpgradeVerificationParams{
		OperationID: mustPgUUID(claim.ID), LeaseToken: mustPgUUID(claim.LeaseToken), Phase: string(j.Phase), Reason: j.Reason, LastObservation: evidence,
		IntervalSeconds: int32(api.RuntimeUpgradeOperationInterval / time.Second), EvidenceExpiresAt: pgtype.Timestamptz{Time: observation.CheckedAt.Add(time.Duration(observation.ValidForSeconds) * time.Second), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict // expired lease/evidence: publish nothing
	}
	if err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("checkpoint verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeVerificationJournal{}, fmt.Errorf("commit verification checkpoint: %w", err)
	}
	return runtimeUpgradeVerificationJournalFromRow(row)
}

func advanceRuntimeUpgradeVerificationDB(ctx context.Context, tx pgx.Tx, op RuntimeUpgradeOperation, j RuntimeUpgradeVerificationJournal) (RuntimeUpgradeVerification, error) {
	out, err := newRuntimeUpgradeVerification(op.ID, j.GatewaySessions, time.Now().UTC())
	if err != nil {
		return RuntimeUpgradeVerification{}, err
	}
	if !j.DeadlineAt.After(out.CheckedAt) {
		out.Reason = "deadline_exceeded"
		return out, nil
	}
	if err := lockRuntimeUpgradeCutoverDB(ctx, tx, op.cutover(op.WakeID)); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			out.Reason = "activation_inputs_changed"
			return out, nil
		}
		return RuntimeUpgradeVerification{}, err
	}
	if _, err := sqlc.New().LockRuntimeReleaseQualification(ctx, tx, op.TargetReleaseID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.Reason = "activation_inputs_changed"
			return out, nil
		}
		return RuntimeUpgradeVerification{}, err
	}
	return runtimeUpgradeVerificationDB(ctx, tx, op.AccountID, out)
}

func runtimeUpgradeVerificationSessions(ids []pgtype.UUID) []string {
	sessions := make([]string, len(ids))
	for i, id := range ids {
		sessions[i] = pgUUIDString(id)
	}
	return sessions
}

func runtimeUpgradeVerificationJournalFromRow(r sqlc.RuntimeUpgradeVerification) (RuntimeUpgradeVerificationJournal, error) {
	j := RuntimeUpgradeVerificationJournal{GatewayRosterRevision: pgUUIDString(r.GatewayRosterRevision), OperationID: pgUUIDString(r.OperationID), GatewaySessions: runtimeUpgradeVerificationSessions(r.GatewaySessions),
		Phase: RuntimeUpgradeVerificationPhase(r.Phase), Reason: r.Reason, CutoverAt: r.CutoverAt.Time.UTC(), CreatedAt: r.CreatedAt.Time.UTC(), DeadlineAt: r.DeadlineAt.Time.UTC(),
		FinishedAt: r.FinishedAt.Time.UTC(), LeaseToken: pgUUIDString(r.LeaseToken), LeaseUntil: r.LeaseUntil.Time.UTC(), NextAttemptAt: r.NextAttemptAt.Time.UTC()}
	if len(r.LastObservation) > 0 && json.Unmarshal(r.LastObservation, &j.LastObservation) != nil {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	return j, nil
}
