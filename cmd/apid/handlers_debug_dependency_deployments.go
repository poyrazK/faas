// Deployment-split dependency comparison (ADR-958 §5): the same rollup as
// the time-split dependency history, with the previous deployment as the
// baseline and the compared deployment as current. This is what turns
// retained app spans into "postgresql SELECT orders p95 82 → 191 ms since
// the previous deployment".

package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugDependencyComparisonMaxItems bounds the per-request evidence copy;
// the dependency endpoint keeps its own output bound.
const debugDependencyComparisonMaxItems = 10

// debugEvidenceComparisonMaxRows bounds the extra read made for one request's
// evidence; the query interleaves deployments, so both sides stay represented.
const debugEvidenceComparisonMaxRows = 500

// debugEvidenceDependencyComparison enriches one request's evidence with its
// route's dependency comparison against the previous deployment. Like the
// regression lookup it is best-effort: a failed read never fails evidence.
func (s *server) debugEvidenceDependencyComparison(r *http.Request, app state.App, acct state.Account, request api.DebugTelemetryRequestItem, from, until time.Time) *api.DebugDependencyDeploymentComparison {
	if request.DeploymentID == "" || request.Route == "" {
		return nil
	}
	rows, err := s.store.ListRequestTelemetryDependencySpans(r.Context(), sqlc.ListRequestTelemetryDependencySpansParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: until, Valid: true},
		Limit:        debugEvidenceComparisonMaxRows,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("debug evidence dependency comparison unavailable", "app_id", app.ID, "err", err)
		}
		return nil
	}
	return buildDebugDependencyDeploymentComparison(rows, request.DeploymentID, request.Route, debugDependencyComparisonMaxItems)
}

type debugDeploymentObservation struct {
	id, tag, commit string
	createdAt       time.Time
}

// observedDeployments returns the deployments with retained spans in rows,
// newest first by creation time (latest retained request as a fallback).
func observedDeployments(rows []sqlc.ListRequestTelemetryDependencySpansRow) []debugDeploymentObservation {
	byID := map[string]*debugDeploymentObservation{}
	for _, row := range rows {
		if row.DeploymentID == "" {
			continue
		}
		obs := byID[row.DeploymentID]
		if obs == nil {
			obs = &debugDeploymentObservation{id: row.DeploymentID, tag: row.DeploymentTag, commit: row.CommitSha}
			if created, err := time.Parse(time.RFC3339Nano, row.DeploymentCreatedAt); err == nil {
				obs.createdAt = created
			}
			byID[row.DeploymentID] = obs
		}
		if row.DeploymentCreatedAt == "" && row.ReceivedAt.Valid && row.ReceivedAt.Time.After(obs.createdAt) {
			obs.createdAt = row.ReceivedAt.Time
		}
	}
	out := make([]debugDeploymentObservation, 0, len(byID))
	for _, obs := range byID {
		out = append(out, *obs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].createdAt.Equal(out[j].createdAt) {
			return out[i].createdAt.After(out[j].createdAt)
		}
		return out[i].id > out[j].id
	})
	return out
}

// buildDebugDependencyDeploymentComparison compares currentID (the newest
// observed deployment when empty) with the newest deployment created before
// it. route, when set, restricts both sides to one stored route ("GET /checkout").
func buildDebugDependencyDeploymentComparison(rows []sqlc.ListRequestTelemetryDependencySpansRow, currentID, route string, maxItems int) *api.DebugDependencyDeploymentComparison {
	scoped := scopeDependencyRows(rows, route)
	deployments := observedDeployments(scoped)
	var current, previous *debugDeploymentObservation
	for i := range deployments {
		if current == nil {
			if currentID == "" || deployments[i].id == currentID {
				current = &deployments[i]
			}
			continue
		}
		previous = &deployments[i]
		break
	}
	if current == nil || previous == nil {
		return nil
	}
	return compareDependencyDeployments(scoped, current, previous, route, maxItems)
}

// buildDebugDependencyComparisonBetween compares two named deployments, as
// the regression detector does with its own current/baseline pair.
func buildDebugDependencyComparisonBetween(rows []sqlc.ListRequestTelemetryDependencySpansRow, currentID, previousID, route string, maxItems int) *api.DebugDependencyDeploymentComparison {
	scoped := scopeDependencyRows(rows, route)
	var current, previous *debugDeploymentObservation
	deployments := observedDeployments(scoped)
	for i := range deployments {
		switch deployments[i].id {
		case currentID:
			current = &deployments[i]
		case previousID:
			previous = &deployments[i]
		}
	}
	if current == nil || previous == nil {
		return nil
	}
	return compareDependencyDeployments(scoped, current, previous, route, maxItems)
}

func scopeDependencyRows(rows []sqlc.ListRequestTelemetryDependencySpansRow, route string) []sqlc.ListRequestTelemetryDependencySpansRow {
	if route == "" {
		return rows
	}
	scoped := make([]sqlc.ListRequestTelemetryDependencySpansRow, 0, len(rows))
	for _, row := range rows {
		if row.Route == route {
			scoped = append(scoped, row)
		}
	}
	return scoped
}

func compareDependencyDeployments(scoped []sqlc.ListRequestTelemetryDependencySpansRow, current, previous *debugDeploymentObservation, route string, maxItems int) *api.DebugDependencyDeploymentComparison {
	items, _, truncated, _, _ := buildDebugDependencyRollup(scoped, func(row sqlc.ListRequestTelemetryDependencySpansRow) (bool, bool) {
		switch row.DeploymentID {
		case current.id:
			return true, true
		case previous.id:
			return true, false
		}
		return false, false
	})
	if maxItems > 0 && len(items) > maxItems {
		items = items[:maxItems]
		truncated = true
	}
	comparison := &api.DebugDependencyDeploymentComparison{
		CurrentDeploymentID: current.id, CurrentDeploymentTag: current.tag, CurrentCommitSHA: current.commit,
		PreviousDeploymentID: previous.id, PreviousDeploymentTag: previous.tag, PreviousCommitSHA: previous.commit,
		Dependencies: items, Truncated: truncated,
	}
	comparison.Route = route
	return comparison
}

// suspectedDependency picks the regressed dependency to name in a route
// regression: a classified dependency (app or platform), never an
// anonymous application span. Items arrive regressions-first by current p95.
func suspectedDependency(comparison *api.DebugDependencyDeploymentComparison) *api.DebugSuspectedDependency {
	if comparison == nil {
		return nil
	}
	for _, item := range comparison.Dependencies {
		if !item.Regression || item.Type == "" || item.Type == "application" {
			continue
		}
		return &api.DebugSuspectedDependency{
			Type: item.Type, Kind: item.Kind, Name: item.Name,
			P95BaseMS: item.BaselineP95MS, P95MS: item.CurrentP95MS, RegressionFactor: item.RegressionFactor,
		}
	}
	return nil
}

// encodeSuspectedDependency returns the jsonb value for the observation row;
// nil keeps the previously stored suspect.
func encodeSuspectedDependency(suspect *api.DebugSuspectedDependency) []byte {
	if suspect == nil {
		return nil
	}
	raw, err := json.Marshal(suspect)
	if err != nil || len(raw) > 1024 {
		return nil
	}
	return raw
}

func parseSuspectedDependency(raw []byte) *api.DebugSuspectedDependency {
	if len(raw) == 0 {
		return nil
	}
	var suspect api.DebugSuspectedDependency
	if err := json.Unmarshal(raw, &suspect); err != nil || suspect.Name == "" {
		return nil
	}
	return &suspect
}
