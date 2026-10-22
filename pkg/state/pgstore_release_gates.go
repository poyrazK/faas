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
		SyntheticCheckID: pgUUIDStringNullable(r.SyntheticCheckID), MaxFailurePercent: int(r.MaxFailurePercent.Int32),
		MaxChangePercent: int(r.MaxChangePercent.Int32),
	}
}

func (s *PgStore) CreateReleaseGate(ctx context.Context, g ReleaseGate, maxGates int, now time.Time) (ReleaseGate, error) {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	slo := g.Kind == ReleaseGateSLO
	newErrors := g.Kind == ReleaseGateNewErrors
	metric := g.Kind == ReleaseGateCustomMetric
	synthetic := g.Kind == ReleaseGateSynthetic
	change := g.Kind == ReleaseGateMetricChange
	sloID, checkID := pgtype.UUID{}, pgtype.UUID{}
	if slo {
		sloID = mustPgUUID(g.SLOID)
	}
	if synthetic {
		checkID = mustPgUUID(g.SyntheticCheckID)
	}
	row, err := sqlc.New().CreateAppReleaseGateWithinLimit(ctx, s.pool, sqlc.CreateAppReleaseGateWithinLimitParams{
		ID: mustPgUUID(g.ID), AccountID: mustPgUUID(g.AccountID), AppID: mustPgUUID(g.AppID), Name: g.Name, Kind: g.Kind,
		SloID: sloID, MaxBurnRate: optionalPgInt4(g.MaxBurnRate, slo),
		MaxNewGroups: optionalPgInt4(g.MaxNewGroups, newErrors), MinEvents: optionalPgInt4(g.MinEvents, newErrors),
		MetricName: optionalPgText(g.MetricName), Comparison: optionalPgText(g.Comparison),
		Threshold: pgtype.Float8{Float64: g.Threshold, Valid: metric}, MinSamples: int32(g.MinSamples),
		CreatedBy: g.CreatedBy, Now: pgTimestamptzUTC(now), MaxGates: int64(maxGates),
		SyntheticCheckID: checkID, MaxFailurePercent: optionalPgInt4(g.MaxFailurePercent, synthetic),
		MaxChangePercent: optionalPgInt4(g.MaxChangePercent, change),
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

func (s *PgStore) ReleaseGateCustomMetricWindow(ctx context.Context, appID, name string, since, until time.Time) (int64, float64, error) {
	row, err := sqlc.New().ReleaseGateCustomMetricWindow(ctx, s.pool, sqlc.ReleaseGateCustomMetricWindowParams{
		AppID: mustPgUUID(appID), Name: name, Since: pgTimestamptzUTC(since), Until: pgTimestamptzUTC(until),
	})
	if err != nil {
		return 0, 0, mapErr(err)
	}
	return row.Samples, row.Average, nil
}

var _ ReleaseGateProbeStore = (*PgStore)(nil)

func (s *PgStore) ListSyntheticGateProbeTargets(ctx context.Context, limit int) ([]SyntheticGateProbeTarget, error) {
	rows, err := sqlc.New().ListSyntheticGateProbeTargets(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]SyntheticGateProbeTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, SyntheticGateProbeTarget{AppID: r.AppID, AccountID: r.AccountID, Slug: r.Slug,
			CandidateID: r.CandidateID, StableID: r.StableID, GateID: r.GateID, Method: r.Method, Path: r.Path,
			ExpectedStatus: int(r.ExpectedStatus.Int32), TimeoutMS: int(r.TimeoutMs)})
	}
	return out, nil
}

func (s *PgStore) RecordReleaseGateProbeResults(ctx context.Context, results []ReleaseGateProbeResult) error {
	q := sqlc.New()
	for _, r := range results {
		if r.Requests < 1 || r.Failures < 0 || r.Failures > r.Requests {
			return ErrInvalidArgument
		}
		if err := q.RecordReleaseGateProbeResult(ctx, s.pool, sqlc.RecordReleaseGateProbeResultParams{
			GateID: mustPgUUID(r.GateID), AppID: mustPgUUID(r.AppID), DeploymentID: mustPgUUID(r.DeploymentID),
			WindowStart: pgTimestamptzUTC(r.WindowStart.Truncate(time.Minute)), Requests: int32(min(r.Requests, releaseGateProbeCap)),
			Failures: int32(min(r.Failures, releaseGateProbeCap)),
		}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func (s *PgStore) ReleaseGateProbeTotals(ctx context.Context, gateID, deploymentID string, since, until time.Time) (int64, int64, error) {
	row, err := sqlc.New().ReleaseGateProbeTotals(ctx, s.pool, sqlc.ReleaseGateProbeTotalsParams{
		GateID: mustPgUUID(gateID), DeploymentID: mustPgUUID(deploymentID), Since: pgTimestamptzUTC(since), Until: pgTimestamptzUTC(until),
	})
	if err != nil {
		return 0, 0, mapErr(err)
	}
	return row.Requests, row.Failures, nil
}

func (s *PgStore) PruneReleaseGateProbeResults(ctx context.Context, before time.Time) error {
	return mapErr(sqlc.New().PruneReleaseGateProbeResults(ctx, s.pool, pgTimestamptzUTC(before)))
}
