package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeVerificationStore = (*PgStore)(nil)
var _ RuntimeUpgradeGatewayStore = (*PgStore)(nil)

func (s *PgStore) RecordRuntimeUpgradeGateway(ctx context.Context, appID, sessionID, candidateID string) error {
	if validateRuntimeAppEnvIDs(appID, sessionID, candidateID) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin gateway runtime receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	cutover, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(candidateID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} // ordinary deployment
	if err != nil {
		return fmt.Errorf("read gateway runtime cutover: %w", err)
	}
	if _, err := q.LockRuntimeUpgradeTargetApp(ctx, tx, mustPgUUID(candidateID)); err != nil {
		return fmt.Errorf("lock gateway runtime app: %w", mapErr(err))
	}
	rows, err := q.ReadRuntimeUpgradeGatewayDeployments(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return fmt.Errorf("read gateway runtime weights: %w", err)
	}
	var deps []Deployment
	var candidate, serving Deployment
	for _, row := range rows {
		d := Deployment{ID: row.ID, AppID: row.AppID, Scope: row.Scope, Status: DeploymentStatus(row.Status), TrafficPercent: int(row.TrafficPercent), TrafficPercentExplicit: row.TrafficPercentExplicit}
		if row.DeletedAt.Valid {
			d.DeletedAt = &row.DeletedAt.Time
		}
		deps = append(deps, d)
		if d.ID == candidateID {
			candidate = d
		}
		if d.ID == pgUUIDString(cutover.ServingDeploymentID) {
			serving = d
		}
	}
	if !runtimeUpgradeActivatedDeployments(deps, candidate, serving) {
		return ErrConflict
	}
	if err := q.PruneRuntimeUpgradeGatewayReceipts(ctx, tx, sqlc.PruneRuntimeUpgradeGatewayReceiptsParams{AppID: mustPgUUID(appID), MaxAgeSeconds: int32(api.RuntimeUpgradeGatewayReceiptMaxAge / time.Second)}); err != nil {
		return fmt.Errorf("prune gateway runtime receipts: %w", err)
	}
	count, err := q.RecordRuntimeUpgradeGatewayReceipt(ctx, tx, sqlc.RecordRuntimeUpgradeGatewayReceiptParams{AppID: mustPgUUID(appID), GatewaySessionID: mustPgUUID(sessionID), DeploymentID: mustPgUUID(candidateID), SessionLimit: api.RuntimeUpgradeGatewaySessionLimit})
	if err != nil {
		return fmt.Errorf("record gateway runtime receipt: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) ListRuntimeUpgradeGatewayRepairApps(ctx context.Context, after string) ([]string, error) {
	if after != "" && validateRuntimeAppEnvIDs(after, after, after) != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListRuntimeUpgradeGatewayRepairApps(ctx, s.pool, sqlc.ListRuntimeUpgradeGatewayRepairAppsParams{AfterAppID: after, MaxAgeSeconds: int32(api.RuntimeUpgradeOperationMaxAge / time.Second), PageLimit: api.RuntimeUpgradeGatewayRepairBatch})
	if err != nil {
		return nil, fmt.Errorf("list gateway runtime repair apps: %w", err)
	}
	return rows, nil
}

func (s *PgStore) PruneExpiredRuntimeUpgradeGatewayReceipts(ctx context.Context) error {
	_, err := sqlc.New().PruneExpiredRuntimeUpgradeGatewayReceipts(ctx, s.pool, sqlc.PruneExpiredRuntimeUpgradeGatewayReceiptsParams{MaxAgeSeconds: int32(api.RuntimeUpgradeGatewayReceiptMaxAge / time.Second), PageLimit: api.RuntimeUpgradeGatewayRepairBatch})
	if err != nil {
		return fmt.Errorf("prune expired gateway runtime receipts: %w", err)
	}
	return nil
}

func (s *PgStore) VerifyRuntimeUpgrade(ctx context.Context, accountID, id string, sessions []string) (RuntimeUpgradeVerification, error) {
	out, err := newRuntimeUpgradeVerification(id, sessions, time.Now().UTC())
	if err != nil || validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerification{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("begin runtime upgrade verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.GetRuntimeUpgradeOperation(ctx, tx, mustPgUUID(id))
	if err != nil {
		return RuntimeUpgradeVerification{}, mapErr(err)
	}
	op := runtimeUpgradeOperationFromRow(row)
	if op.AccountID != accountID {
		return RuntimeUpgradeVerification{}, ErrNotFound
	}
	if op.Phase != RuntimeUpgradeComplete {
		out.Reason = "activation_pending"
		return out, nil
	}
	c, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(op.DeploymentID))
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification cutover: %w", mapErr(err))
	}
	cutover := runtimeUpgradeCutoverFromRow(c)
	if !op.cutover(op.WakeID).matches(cutover) {
		out.Reason = "activation_changed"
		return out, nil
	}
	candidate, err := runtimeUpgradeVerificationInputsDB(ctx, tx, op)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		out.Reason = "activation_inputs_changed"
		return out, nil
	}
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification inputs: %w", err)
	}
	rows, err := q.ReadRuntimeUpgradeGatewayReceipts(ctx, tx, mustPgUUID(op.AppID))
	if err != nil {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification gateway receipts: %w", err)
	}
	receipts := make([]runtimeUpgradeGatewayReceipt, 0, len(rows))
	for _, row := range rows {
		receipts = append(receipts, runtimeUpgradeGatewayReceipt{SessionID: pgUUIDString(row.GatewaySessionID), DeploymentID: pgUUIDString(row.DeploymentID), CutoverAt: row.CutoverAt.Time, InstalledAt: row.InstalledAt.Time})
	}
	var health *api.AppHealthResponse
	assessment, err := q.ReadAppHealthCollection(ctx, tx, sqlc.ReadAppHealthCollectionParams{AppID: op.AppID, AccountID: accountID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeVerification{}, fmt.Errorf("read verification health: %w", err)
	}
	if err == nil && len(assessment) > 0 {
		if json.Unmarshal(assessment, &health) != nil {
			return RuntimeUpgradeVerification{}, ErrConflict
		}
	}
	out.CheckedAt = time.Now().UTC()
	return evaluateRuntimeUpgradeVerification(out, cutover, candidate.Scope, receipts, health, op.AppID), nil
}

func runtimeUpgradeVerificationInputsDB(ctx context.Context, tx pgx.Tx, op RuntimeUpgradeOperation) (Deployment, error) {
	row, err := sqlc.New().GetDeploymentRuntimeUpgradeBaseline(ctx, tx, mustPgUUID(op.DeploymentID))
	if err != nil {
		return Deployment{}, mapErr(err)
	}
	baseline := runtimeUpgradeBaselineFromRow(row)
	current, err := runtimeUpgradeBaselineDBMode(ctx, tx, op.DeploymentID, op.ServingDeploymentID, true)
	if err != nil {
		return Deployment{}, err
	}
	if !sameRuntimeUpgradeBaseline(baseline, current) {
		return Deployment{}, ErrConflict
	}
	candidate, target, qualification, err := runtimeUpgradeAcceptanceInputsDB(ctx, tx, op.DeploymentID, baseline, false)
	if err != nil {
		return Deployment{}, err
	}
	if qualification.ReportSHA256 != op.QualificationReportSHA256 || !validRuntimeReleaseQualification(target, qualification, time.Now().UTC()) {
		return Deployment{}, ErrConflict
	}
	return candidate, nil
}
