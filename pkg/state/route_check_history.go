package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type RouteCheckHistoryStore interface {
	ListRouteCheckHistory(context.Context, string, string, string, int, string) (api.RouteCheckHistoryPage, error)
	GetRouteCheckHistoryEntry(context.Context, string, string, string, string) (api.RouteCheckHistoryEntry, error)
}

func buildRouteCheckHistory(claim AutomaticRouteCheckClaim, check api.RouteRequirementsCheck, baseline api.RouteCheckFindingBaseline, eligible bool, at time.Time) (api.RouteCheckHistoryEntry, []byte, []byte, error) {
	changes, next := api.CompareRouteChecks(baseline, check, claim.RequestID, eligible)
	entry := api.RouteCheckHistoryEntry{Version: 1, ID: claim.RequestID, CheckedAt: at, Check: check, Changes: changes}
	body, err := json.Marshal(entry)
	if err != nil {
		return entry, nil, nil, fmt.Errorf("encode route check history: %w", err)
	}
	if len(body) > api.RouteCheckHistoryEntryMaxBytes {
		return entry, nil, nil, ErrInvalidArgument
	}
	known, err := json.Marshal(next)
	if err != nil {
		return entry, nil, nil, fmt.Errorf("encode route finding baseline: %w", err)
	}
	return entry, body, known, nil
}

func validateRouteHistoryPage(limit int, before string) error {
	if limit < 1 || limit > api.RouteCheckHistoryMaxPage {
		return ErrInvalidArgument
	}
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			return ErrInvalidArgument
		}
	}
	return nil
}

func decodeRouteHistoryEntry(body []byte, appID, deploymentID string) (api.RouteCheckHistoryEntry, error) {
	var entry api.RouteCheckHistoryEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return entry, fmt.Errorf("decode route check history: %w", err)
	}
	if entry.Version != 1 || entry.ID == "" || entry.Check.AppID != appID || entry.Check.DeploymentID != deploymentID || entry.Changes.CheckID != entry.ID || entry.CheckedAt.IsZero() {
		return entry, fmt.Errorf("invalid route check history identity: %w", ErrInvalidArgument)
	}
	return entry, nil
}

func routeHistorySummary(entry api.RouteCheckHistoryEntry) api.RouteCheckHistorySummary {
	return api.RouteCheckHistorySummary{Version: entry.Version, ID: entry.ID, CheckedAt: entry.CheckedAt, Status: entry.Check.Report.Status, RequirementsRevision: entry.Check.RequirementsRevision, RequirementsSHA256: entry.Check.RequirementsSHA256, ComparisonStatus: entry.Changes.Status, Summary: entry.Changes.Summary}
}
