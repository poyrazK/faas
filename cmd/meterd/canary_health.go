package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const canaryHealthTelemetryRowLimit = 10000

type canaryLatencyWeight struct {
	latency int32
	weight  int64
}

type canaryHealthTelemetrySummaryReader interface {
	RequestTelemetryCircuitBreakerSummary(ctx context.Context, appID, deploymentID string, since, until time.Time) (requests, serverErrors int64, p95LatencyMS float64, coldBootRequests int64, coldBootP95LatencyMS float64, cpuUsec, cpuRequests int64, err error)
}

// CircuitBreakerObservation reads bounded per-deployment request telemetry
// for the candidate and its sole serving predecessor, and checks the
// candidate's platform OOM counter. The request telemetry reader is internal
// to meterd; the customer-facing debug telemetry plan gate does not apply.
func (a *canaryStoreAdapter) CircuitBreakerObservation(ctx context.Context, row canary.CanaryRow, since, now time.Time) (canary.CircuitBreakerObservation, error) {
	if a.store == nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: nil store")
	}
	if since.IsZero() || !since.Before(now) {
		since = now.Add(-5 * time.Minute)
	}

	appID, err := pgUUID(row.AppID)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: parse app id: %w", err)
	}
	candidateID, err := pgUUID(row.ID)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: parse candidate id: %w", err)
	}

	out := canary.CircuitBreakerObservation{}
	live, err := a.store.LiveDeployments(ctx, row.AppID)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: list live deployments: %w", err)
	}
	var stable *state.Deployment
	for i := range live {
		if live[i].ID == row.ID || live[i].Scope != row.Scope || live[i].TrafficPercent <= 0 {
			continue
		}
		if stable != nil {
			return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: multiple serving predecessors for deployment %s", row.ID)
		}
		stable = &live[i]
	}
	if stable != nil {
		stableID, parseErr := pgUUID(stable.ID)
		if parseErr != nil {
			return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: parse stable deployment id: %w", parseErr)
		}
		out.StableDeploymentID = stable.ID
		out.HasStable = true
		out.Stable, err = a.deploymentHealthWindow(ctx, appID, stableID, since, now)
		if err != nil {
			return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: read stable telemetry: %w", err)
		}
	}
	if stable == nil {
		// A first release has no safe rollback target. The canary still uses
		// its startup-readiness and smoke checks, but cannot run a comparison
		// breaker that promises to restore a prior revision.
		out.OOMSignalAvailable = true
		return out, nil
	}
	out.Candidate, err = a.deploymentHealthWindow(ctx, appID, candidateID, since, now)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: read candidate telemetry: %w", err)
	}
	_, out.CPURequestSignalAvailable = a.store.(canaryHealthTelemetrySummaryReader)

	if a.promQL == nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: Prometheus client is not configured")
	}
	coverageQuery := `(count({__name__=~".*_workload_oom_kills_total"}) or vector(0))`
	coverage, err := a.promQL.QueryScalar(ctx, coverageQuery)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: query workload OOM metric coverage: %w", err)
	}
	if coverage <= 0 {
		// Do not treat a missing metric family as a healthy zero. The metrics
		// registry exports an always-present sentinel series when its OOM
		// instrumentation is online.
		return out, nil
	}
	rolloutSince := row.RolloutStarted
	if rolloutSince.IsZero() || !rolloutSince.Before(now) {
		rolloutSince = since
	}
	window := now.Sub(rolloutSince)
	if window < time.Minute {
		window = time.Minute
	}
	if window > 10*time.Minute {
		window = 10 * time.Minute
	}
	query := fmt.Sprintf(
		`(sum(increase({__name__=~".*_workload_oom_kills_total",app=%s,deployment=%s}[%ds])) or vector(0))`,
		strconv.Quote(row.AppID), strconv.Quote(row.ID), int(window.Seconds()))
	out.OOMKills, err = a.promQL.QueryScalar(ctx, query)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: query candidate OOM counter: %w", err)
	}
	out.OOMSignalAvailable = true

	dependencyCoverageQuery := `(count({__name__=~".*_service_dependency_calls_total"}) or vector(0))`
	dependencyCoverage, err := a.promQL.QueryScalar(ctx, dependencyCoverageQuery)
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: query managed dependency metric coverage: %w", err)
	}
	if dependencyCoverage <= 0 {
		return out, nil
	}
	candidateCalls, candidateErrors, err := a.dependencyCallSummary(ctx, row.AppID, row.ID, int(window.Seconds()))
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: read candidate managed dependency counters: %w", err)
	}
	stableCalls, stableErrors, err := a.dependencyCallSummary(ctx, row.AppID, out.StableDeploymentID, int(window.Seconds()))
	if err != nil {
		return canary.CircuitBreakerObservation{}, fmt.Errorf("canary health: read stable managed dependency counters: %w", err)
	}
	out.Candidate.DependencyCalls = candidateCalls
	out.Candidate.DependencyErrors = candidateErrors
	out.Stable.DependencyCalls = stableCalls
	out.Stable.DependencyErrors = stableErrors
	out.DependencySignalAvailable = true
	return out, nil
}

func (a *canaryStoreAdapter) dependencyCallSummary(ctx context.Context, appID, deploymentID string, windowSeconds int) (calls, errors int64, err error) {
	if windowSeconds <= 0 || appID == "" || deploymentID == "" {
		return 0, 0, fmt.Errorf("invalid managed dependency query window or identity")
	}
	base := fmt.Sprintf(
		`__name__=~".*_service_dependency_calls_total",app=%s,deployment=%s`,
		strconv.Quote(appID), strconv.Quote(deploymentID))
	window := fmt.Sprintf("[%ds]", windowSeconds)
	callQuery := fmt.Sprintf(`(sum(increase({%s}%s)) or vector(0))`, base, window)
	errorQuery := fmt.Sprintf(`(sum(increase({%s,outcome="error"}%s)) or vector(0))`, base, window)
	callValue, err := a.promQL.QueryScalar(ctx, callQuery)
	if err != nil {
		return 0, 0, err
	}
	errorValue, err := a.promQL.QueryScalar(ctx, errorQuery)
	if err != nil {
		return 0, 0, err
	}
	return int64(math.Round(math.Max(0, callValue))), int64(math.Round(math.Max(0, errorValue))), nil
}

func (a *canaryStoreAdapter) deploymentHealthWindow(ctx context.Context, appID, deploymentID pgtype.UUID, since, now time.Time) (canary.HealthWindow, error) {
	if reader, ok := a.store.(canaryHealthTelemetrySummaryReader); ok {
		requests, serverErrors, p95, coldBootRequests, coldBootP95, cpuUsec, cpuRequests, err := reader.RequestTelemetryCircuitBreakerSummary(ctx, uuid.UUID(appID.Bytes).String(), uuid.UUID(deploymentID.Bytes).String(), since, now)
		if err != nil {
			return canary.HealthWindow{}, err
		}
		return canary.HealthWindow{
			Requests:             requests,
			ServerErrors:         serverErrors,
			P95LatencyMS:         p95,
			ColdBootRequests:     coldBootRequests,
			ColdBootP95LatencyMS: coldBootP95,
			CPUUsec:              cpuUsec,
			CPURequests:          cpuRequests,
		}, nil
	}
	rows, err := a.store.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{
		AppID:        appID,
		DeploymentID: deploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:        canaryHealthTelemetryRowLimit,
	})
	if err != nil {
		return canary.HealthWindow{}, err
	}
	var out canary.HealthWindow
	latencies := make([]canaryLatencyWeight, 0, len(rows))
	coldBootLatencies := make([]canaryLatencyWeight, 0, len(rows))
	for _, row := range rows {
		weight := int64(row.Count)
		if weight <= 0 {
			continue
		}
		out.Requests += weight
		if row.Status >= 500 && row.Status < 600 {
			out.ServerErrors += weight
		}
		sample := canaryLatencyWeight{latency: row.LatencyMs, weight: weight}
		latencies = append(latencies, sample)
		if row.ColdBoot {
			out.ColdBootRequests += weight
			coldBootLatencies = append(coldBootLatencies, sample)
		}
	}
	if out.Requests == 0 {
		return out, nil
	}
	out.P95LatencyMS = weightedP95(latencies, out.Requests)
	if out.ColdBootRequests > 0 {
		out.ColdBootP95LatencyMS = weightedP95(coldBootLatencies, out.ColdBootRequests)
	}
	return out, nil
}

func weightedP95(latencies []canaryLatencyWeight, requests int64) float64 {
	sort.Slice(latencies, func(i, j int) bool { return latencies[i].latency < latencies[j].latency })
	target := int64(math.Ceil(float64(requests)*0.95)) - 1
	if target < 0 {
		target = 0
	}
	var cumulative int64
	for _, sample := range latencies {
		cumulative += sample.weight
		if cumulative > target {
			return float64(sample.latency)
		}
	}
	return 0
}

func pgUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
