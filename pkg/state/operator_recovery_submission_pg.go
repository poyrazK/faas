// adr: 1524
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OperatorRecoverySubmissionStore = (*PgStore)(nil)

func (s *PgStore) SubmitOperatorRecovery(ctx context.Context, in OperatorRecoverySubmission) (OperatorRecoveryReceipt, error) {
	in, err := NormalizeOperatorRecovery(in)
	if err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	key, digest := operatorRecoveryDigests(in)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("begin recovery submission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	actor := operatorRestartUUID(in.ActorID)
	if err := q.LockOperatorRecoverySubmission(ctx, tx, sqlc.LockOperatorRecoverySubmissionParams{ActorID: actor, KeyDigest: key}); err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("lock recovery submission: %w", err)
	}
	row, err := q.ReadOperatorRecoverySubmission(ctx, tx, sqlc.ReadOperatorRecoverySubmissionParams{ActorID: actor, KeyDigest: key})
	if err == nil {
		return replayOperatorRecovery(OperatorRecoveryReceipt{IntentID: pgUUIDString(row.IntentID), TargetID: row.TargetID,
			Kind: OperatorIntentKind(row.Kind), ResponseBody: row.ResponseBody, AuditOutboxID: row.AuditOutboxID}, row.RequestDigest, digest)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperatorRecoveryReceipt{}, fmt.Errorf("read recovery submission: %w", err)
	}
	legacy, err := q.OperatorRecoveryLegacyKeyExists(ctx, tx, sqlc.OperatorRecoveryLegacyKeyExistsParams{ActorID: actor, RequestKey: in.RequestKey})
	if err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("check legacy recovery submission: %w", err)
	}
	if legacy {
		return OperatorRecoveryReceipt{}, ErrOperatorRecoveryLegacyKey
	}
	target, err := readOperatorRecoveryTarget(ctx, tx, in)
	if err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	if err := operatorRecoveryEligible(in, target); err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	receipt, audit, err := newOperatorRecoveryReceipt(in, target, time.Now().UTC())
	if err != nil {
		return receipt, err
	}
	var trace pgtype.Text
	if in.TraceID != nil {
		trace = pgtype.Text{String: *in.TraceID, Valid: true}
	}
	if _, err := q.InsertOperatorRecoveryIntent(ctx, tx, sqlc.InsertOperatorRecoveryIntentParams{
		ID: operatorRestartUUID(receipt.IntentID), Kind: string(in.Kind), TargetID: receipt.TargetID,
		AccountID: operatorRestartUUID(target.AccountID), ActorID: actor, Reason: in.Reason, TraceID: trace,
	}); err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("insert recovery intent: %w", err)
	}
	receipt.AuditOutboxID, err = q.EnqueueOperatorRecoveryRequestAudit(ctx, tx, sqlc.EnqueueOperatorRecoveryRequestAuditParams{
		Kind: OperatorRecoveryAuditKind(in.Kind), Subject: operatorRestartUUID(target.AccountID), Data: audit, DedupeKey: "operator-recovery-request:" + receipt.IntentID,
	})
	if err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("enqueue recovery request audit: %w", err)
	}
	if _, err := q.InsertOperatorRecoverySubmission(ctx, tx, sqlc.InsertOperatorRecoverySubmissionParams{
		ActorID: actor, KeyDigest: key, RequestDigest: digest, IntentID: operatorRestartUUID(receipt.IntentID),
		ResponseBody: receipt.ResponseBody, AuditOutboxID: receipt.AuditOutboxID,
	}); err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("insert recovery receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorRecoveryReceipt{}, fmt.Errorf("commit recovery submission: %w", err)
	}
	return receipt, nil
}

func readOperatorRecoveryTarget(ctx context.Context, tx pgx.Tx, in OperatorRecoverySubmission) (operatorRecoveryTarget, error) {
	q := sqlc.New()
	if in.Kind != OperatorIntentKindForceColdBoot {
		row, err := q.ReadOperatorRecoveryInstance(ctx, tx, operatorRestartUUID(in.Target))
		return operatorRecoveryTarget{row.InstanceID, row.AppID, row.DeploymentID, row.AccountID, row.State}, mapErr(err)
	}
	app, err := q.ReadOperatorRecoveryApp(ctx, tx, in.Target)
	if err != nil {
		return operatorRecoveryTarget{}, mapErr(err)
	}
	deployment, err := q.ReadOperatorRecoveryDeployment(ctx, tx, operatorRestartUUID(app.AppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return operatorRecoveryTarget{}, ErrOperatorRecoveryDeployment
	}
	return operatorRecoveryTarget{AppID: app.AppID, AccountID: app.AccountID, DeploymentID: deployment}, err
}
