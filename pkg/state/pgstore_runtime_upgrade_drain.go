package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeGatewayDrainStore = (*PgStore)(nil)
var _ RuntimeUpgradeDrainVerificationStore = (*PgStore)(nil)

func (s *PgStore) RuntimeUpgradeGatewayDrainSnapshot(ctx context.Context, appID string) (RuntimeUpgradeDrainSnapshot, error) {
	if !canonicalRuntimeUpgradeGatewayUUID(appID) {
		return RuntimeUpgradeDrainSnapshot{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RuntimeUpgradeDrainSnapshot{}, fmt.Errorf("begin gateway drain snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return runtimeUpgradeDrainSnapshotDB(ctx, tx, appID)
}

func runtimeUpgradeDrainSnapshotDB(ctx context.Context, tx pgx.Tx, appID string) (RuntimeUpgradeDrainSnapshot, error) {
	q := sqlc.New()
	rows, err := q.ReadRuntimeUpgradeDrainDeployments(ctx, tx, sqlc.ReadRuntimeUpgradeDrainDeploymentsParams{AppID: mustPgUUID(appID), Limit: api.RuntimeUpgradeDrainDeploymentLimit + 1})
	if err != nil {
		return RuntimeUpgradeDrainSnapshot{}, fmt.Errorf("read gateway drain routing rows: %w", err)
	}
	if len(rows) > api.RuntimeUpgradeDrainDeploymentLimit {
		return RuntimeUpgradeDrainSnapshot{}, ErrConflict
	}
	hash := sha256.New()
	deps := make([]Deployment, 0, len(rows))
	for _, r := range rows {
		if !canonicalRuntimeUpgradeGatewayUUID(r.ID) || !canonicalRuntimeUpgradeGatewayUUID(r.RuntimeUpgradeRoutingToken) {
			return RuntimeUpgradeDrainSnapshot{}, ErrConflict
		}
		_, _ = fmt.Fprintf(hash, "%s:%s\n", r.ID, r.RuntimeUpgradeRoutingToken)
		d := Deployment{ID: r.ID, AppID: r.AppID, Scope: r.Scope, Status: DeploymentStatus(r.Status), TrafficPercent: int(r.TrafficPercent), TrafficPercentExplicit: r.TrafficPercentExplicit}
		if r.DeletedAt.Valid {
			d.DeletedAt = &r.DeletedAt.Time
		}
		deps = append(deps, d)
	}
	out := RuntimeUpgradeDrainSnapshot{Deployments: ProductionRoutingDeployments(deps), RoutingRevision: hex.EncodeToString(hash.Sum(nil))}
	for _, d := range out.Deployments {
		if d.TrafficPercent != 100 || d.DeletedAt != nil {
			continue
		}
		plan, readErr := runtimeUpgradeDrainPlanDB(ctx, tx, deps, d)
		if readErr != nil {
			return RuntimeUpgradeDrainSnapshot{}, readErr
		}
		out.Plan = plan
		break
	}
	return out, nil
}

func runtimeUpgradeDrainPlanDB(ctx context.Context, tx pgx.Tx, deps []Deployment, candidate Deployment) (*RuntimeUpgradeDrainPlan, error) {
	q := sqlc.New()
	c, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(candidate.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read gateway drain cutover: %w", err)
	}
	r, err := q.GetRuntimeUpgradeOperationForDeployment(ctx, tx, mustPgUUID(candidate.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read gateway drain operation: %w", err)
	}
	op := runtimeUpgradeOperationFromRow(r)
	cutover := runtimeUpgradeCutoverFromRow(c)
	if op.Phase != RuntimeUpgradeComplete || op.AppID != candidate.AppID || !op.cutover(op.WakeID).matches(cutover) {
		return nil, nil
	}
	var previous Deployment
	for _, d := range deps {
		if d.ID == cutover.ServingDeploymentID {
			previous = d
			break
		}
	}
	if !runtimeUpgradeActivatedDeployments(deps, candidate, previous) {
		return nil, nil
	}
	plan := &RuntimeUpgradeDrainPlan{OperationID: op.ID, DeploymentID: candidate.ID, ServingDeploymentID: previous.ID, CutoverAt: cutover.CutoverAt}
	roster, err := q.ReadRuntimeUpgradeGatewayRoster(ctx, tx)
	if err == nil {
		plan.GatewayRosterRevision = pgUUIDString(roster.Revision)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read gateway drain roster: %w", err)
	}
	return plan, nil
}

func (s *PgStore) RecordRuntimeUpgradeGatewayDrain(ctx context.Context, o RuntimeUpgradeGatewayDrainObservation) error {
	if err := o.validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin gateway drain receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.ShareRuntimeUpgradeGatewayRosterHead(ctx, tx); err != nil {
		return fmt.Errorf("fence gateway drain membership: %w", err)
	}
	if _, err = q.LockRuntimeUpgradeTargetApp(ctx, tx, mustPgUUID(o.DeploymentID)); err != nil {
		return fmt.Errorf("fence gateway drain app: %w", mapErr(err))
	}
	if _, err = q.LockRuntimeUpgradeDrainDeployments(ctx, tx, sqlc.LockRuntimeUpgradeDrainDeploymentsParams{AppID: mustPgUUID(o.AppID), Limit: api.RuntimeUpgradeDrainDeploymentLimit + 1}); err != nil {
		return fmt.Errorf("fence gateway drain routing: %w", runtimeUpgradeBaselineError(err))
	}
	snapshot, err := runtimeUpgradeDrainSnapshotDB(ctx, tx, o.AppID)
	if err != nil {
		return err
	}
	if snapshot.RoutingRevision != o.RoutingRevision || snapshot.Plan == nil || !snapshot.Plan.matches(o.RuntimeUpgradeDrainPlan) {
		return ErrConflict
	}
	if err = q.PruneAppRuntimeUpgradeGatewayDrains(ctx, tx, mustPgUUID(o.AppID)); err != nil {
		return fmt.Errorf("prune app gateway drains: %w", err)
	}
	n, err := q.RecordRuntimeUpgradeGatewayDrain(ctx, tx, sqlc.RecordRuntimeUpgradeGatewayDrainParams{
		AppID: mustPgUUID(o.AppID), GatewaySessionID: mustPgUUID(o.SessionID), SlotID: mustPgUUID(o.SlotID), OperationID: mustPgUUID(o.OperationID),
		DeploymentID: mustPgUUID(o.DeploymentID), ServingDeploymentID: mustPgUUID(o.ServingDeploymentID), GatewayRosterRevision: mustPgUUID(o.GatewayRosterRevision),
		RoutingRevision: o.RoutingRevision, FenceID: mustPgUUID(o.FenceID), ActivityVersion: o.ActivityVersion, CutoverAt: pgtype.Timestamptz{Time: o.CutoverAt, Valid: true},
		LeaseSeconds: int32(api.RuntimeUpgradeDrainReceiptMaxAge / time.Second), SessionLimit: api.RuntimeUpgradeGatewaySessionLimit})
	if err != nil {
		return fmt.Errorf("record gateway drain receipt: %w", runtimeUpgradeBaselineError(err))
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) ListRuntimeUpgradeGatewayDrainRepairApps(ctx context.Context, after string) ([]string, error) {
	if after != "" && !canonicalRuntimeUpgradeGatewayUUID(after) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListRuntimeUpgradeGatewayDrainRepairApps(ctx, s.pool, sqlc.ListRuntimeUpgradeGatewayDrainRepairAppsParams{AfterAppID: after, MaxAgeSeconds: int32(api.RuntimeUpgradeOperationMaxAge / time.Second), PageLimit: api.RuntimeUpgradeGatewayRepairBatch})
	if err != nil {
		return nil, fmt.Errorf("read gateway drain repair page: %w", err)
	}
	return rows, nil
}

func (s *PgStore) PruneExpiredRuntimeUpgradeGatewayDrains(ctx context.Context) error {
	_, err := sqlc.New().PruneRuntimeUpgradeGatewayDrains(ctx, s.pool, api.RuntimeUpgradeGatewayRepairBatch)
	if err != nil {
		return fmt.Errorf("prune gateway drain receipts: %w", err)
	}
	return nil
}
