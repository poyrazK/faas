package routeinsights

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ListStore is the surface the insights listing reads.
type ListStore interface {
	ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error)
	ListEdgeRulesForApp(ctx context.Context, appID string) ([]state.EdgeRule, error)
}

// List returns an app's active insights, then those resolved within
// RouteInsightResolvedListWindow of now. Each active insight carries the fix
// applying it would make now (ADR-20261020161310).
func List(ctx context.Context, store ListStore, accountID, appID string, now time.Time) ([]api.RouteInsight, error) {
	rows, err := store.ListRouteInsights(ctx, sqlc.ListRouteInsightsParams{
		AppID: pgUUID(appID), AccountID: pgUUID(accountID),
		ResolvedSince: timestamptz(now.Add(-api.RouteInsightResolvedListWindow)), RowLimit: api.RouteInsightListLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list route insights: %w", err)
	}
	out := make([]api.RouteInsight, 0, len(rows))
	for _, row := range rows {
		out = append(out, insightFromRow(row))
	}
	if len(out) == 0 {
		return out, nil
	}
	rules, err := store.ListEdgeRulesForApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("list edge rules: %w", err)
	}
	active := activeInsights(out)
	for i := range out {
		if plan, _ := PlanFix(out[i], active, rules, now); plan != nil {
			fix := plan.Fix
			out[i].Fix = &fix
		}
	}
	return out, nil
}

func insightFromRow(row sqlc.ListRouteInsightsRow) api.RouteInsight {
	insight := api.RouteInsight{
		ID: uuidString(row.ID), Kind: row.Kind, Method: row.Method, Route: row.Route, State: row.State, Message: row.Message,
		Evidence:        json.RawMessage(row.Evidence),
		FirstDetectedAt: row.FirstDetectedAt.Time.UTC().Format(time.RFC3339),
		LastDetectedAt:  row.LastDetectedAt.Time.UTC().Format(time.RFC3339),
	}
	if row.ResolvedAt.Valid {
		insight.ResolvedAt = row.ResolvedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.FixedAt.Valid {
		insight.FixedAt = row.FixedAt.Time.UTC().Format(time.RFC3339)
		insight.FixAction = row.FixAction.String
	}
	if len(insight.Evidence) == 0 {
		insight.Evidence = json.RawMessage("{}")
	}
	return insight
}

func activeInsights(all []api.RouteInsight) []api.RouteInsight {
	var out []api.RouteInsight
	for _, insight := range all {
		if insight.State == api.RouteInsightActive {
			out = append(out, insight)
		}
	}
	return out
}
