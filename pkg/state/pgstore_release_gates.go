package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ ReleaseGateStore         = (*PgStore)(nil)
	_ ReleaseGateEvidenceStore = (*PgStore)(nil)
)

func releaseGateFromRow(r sqlc.AppReleaseGate) ReleaseGate {
	return ReleaseGate{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID),
		Name: r.Name, Kind: r.Kind, SLOID: pgUUIDStringNullable(r.SloID),
		MaxBurnRate: int(r.MaxBurnRate.Int32), MaxNewGroups: int(r.MaxNewGroups.Int32), MinEvents: int(r.MinEvents.Int32),
		MetricName: r.MetricName.String, Comparison: r.Comparison.String, Threshold: r.Threshold.Float64,
		MinSamples: int(r.MinSamples), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.Time.UTC(),
	}
}

func (s *PgStore) CreateReleaseGate(ctx context.Context, g ReleaseGate, maxGates int, now time.Time) (ReleaseGate, error) {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	slo := g.Kind == ReleaseGateSLO
	newErrors := g.Kind == ReleaseGateNewErrors
	metric := g.Kind == ReleaseGateCustomMetric
	// An ai gate keeps its largest increase in threshold (ADR 20261022153155).
	threshold := metric || g.Kind == ReleaseGateAI
	sloID := pgtype.UUID{}
	if slo {
		sloID = mustPgUUID(g.SLOID)
	}
	row, err := sqlc.New().CreateAppReleaseGateWithinLimit(ctx, s.pool, sqlc.CreateAppReleaseGateWithinLimitParams{
		ID: mustPgUUID(g.ID), AccountID: mustPgUUID(g.AccountID), AppID: mustPgUUID(g.AppID), Name: g.Name, Kind: g.Kind,
		SloID: sloID, MaxBurnRate: optionalPgInt4(g.MaxBurnRate, slo),
		MaxNewGroups: optionalPgInt4(g.MaxNewGroups, newErrors), MinEvents: optionalPgInt4(g.MinEvents, newErrors),
		MetricName: optionalPgText(g.MetricName), Comparison: optionalPgText(g.Comparison),
		Threshold: pgtype.Float8{Float64: g.Threshold, Valid: threshold}, MinSamples: int32(g.MinSamples),
		CreatedBy: g.CreatedBy, Now: pgTimestamptzUTC(now), MaxGates: int64(maxGates),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseGate{}, ErrReleaseGateLimit
	}
	if err != nil {
		return ReleaseGate{}, mapErr(err)
	}
	return releaseGateFromRow(row), nil
}

func (s *PgStore) ListReleaseGates(ctx context.Context, appID string) ([]ReleaseGate, error) {
	rows, err := sqlc.New().ListAppReleaseGates(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseGate, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseGateFromRow(r))
	}
	return out, nil
}

func (s *PgStore) DeleteReleaseGate(ctx context.Context, appID, name string) (ReleaseGate, error) {
	row, err := sqlc.New().DeleteAppReleaseGate(ctx, s.pool, sqlc.DeleteAppReleaseGateParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseGate{}, ErrNotFound
	}
	if err != nil {
		return ReleaseGate{}, mapErr(err)
	}
	return releaseGateFromRow(row), nil
}

func (s *PgStore) ReleaseGateSLO(ctx context.Context, appID, sloID string) (ReleaseGateSLOTarget, error) {
	if _, err := uuid.Parse(sloID); err != nil {
		return ReleaseGateSLOTarget{}, ErrNotFound
	}
	row, err := sqlc.New().AppSLOForReleaseGate(ctx, s.pool, sqlc.AppSLOForReleaseGateParams{ID: mustPgUUID(sloID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseGateSLOTarget{}, ErrNotFound
	}
	if err != nil {
		return ReleaseGateSLOTarget{}, mapErr(err)
	}
	return ReleaseGateSLOTarget{ID: pgUUIDString(row.ID), SLI: row.Sli, LatencyThresholdMS: int(row.LatencyThresholdMs.Int32), ObjectiveBP: int(row.ObjectiveBp)}, nil
}

func (s *PgStore) ReleaseGateDeploymentSLI(ctx context.Context, appID, deploymentID string, since, until time.Time, latencyThresholdMS int) (ReleaseGateSLISample, error) {
	row, err := sqlc.New().ReleaseGateDeploymentSLI(ctx, s.pool, sqlc.ReleaseGateDeploymentSLIParams{
		LatencyThresholdMs: int32(latencyThresholdMS), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
		Since: pgTimestamptzUTC(since), Until: pgTimestamptzUTC(until),
	})
	if err != nil {
		return ReleaseGateSLISample{}, mapErr(err)
	}
	return ReleaseGateSLISample{Requests: row.Requests, ServerErrors: row.ServerErrors, SlowRequests: row.SlowRequests}, nil
}

func (s *PgStore) ReleaseGateNewErrorGroups(ctx context.Context, appID, deploymentID string, minEvents int) ([]ReleaseGateErrorGroup, error) {
	rows, err := sqlc.New().ReleaseGateNewErrorGroups(ctx, s.pool, sqlc.ReleaseGateNewErrorGroupsParams{
		AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID), MinEvents: int64(minEvents),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ReleaseGateErrorGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReleaseGateErrorGroup{ErrorClass: r.ErrorClass, Events: r.Events})
	}
	return out, nil
}

// ReleaseGateDeploymentAI sums a deployment's AI minute rows in [since,
// until) for an ai gate (ADR 20261022153155).
func (s *PgStore) ReleaseGateDeploymentAI(ctx context.Context, appID, deploymentID string, since, until time.Time) (ReleaseGateAIWindow, error) {
	if !validUUID(deploymentID) {
		return ReleaseGateAIWindow{}, nil
	}
	row, err := sqlc.New().ReleaseGateDeploymentAI(ctx, s.pool, sqlc.ReleaseGateDeploymentAIParams{
		DeploymentID: mustPgUUID(deploymentID), AppID: mustPgUUID(appID), Since: pgTimestamptzUTC(since), Until: pgTimestamptzUTC(until),
	})
	if err != nil {
		return ReleaseGateAIWindow{}, mapErr(err)
	}
	return ReleaseGateAIWindow{Calls: row.Calls, ErrorCalls: row.ErrorCalls, FlaggedCalls: row.FlaggedCalls,
		CostMillicents: row.CostMillicents, TTFBCalls: row.TtfbCalls, TTFBMSTotal: row.TtfbMsTotal}, nil
}

func (s *PgStore) ReleaseGateCustomMetricWindow(ctx context.Context, appID, name string, since, until time.Time) (int64, float64, error) {
	row, err := sqlc.New().ReleaseGateCustomMetricWindow(ctx, s.pool, sqlc.ReleaseGateCustomMetricWindowParams{
		AppID: mustPgUUID(appID), Name: name, Since: pgTimestamptzUTC(since), Until: pgTimestamptzUTC(until),
	})
	if err != nil {
		return 0, 0, mapErr(err)
	}
	return row.Samples, row.Average, nil
}
