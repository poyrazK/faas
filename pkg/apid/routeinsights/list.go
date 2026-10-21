package routeinsights

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ListStore is the surface the insights listing reads.
type ListStore interface {
	ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error)
}

// List returns an app's active insights, then those resolved within
// RouteInsightResolvedListWindow of now.
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
		insight := api.RouteInsight{
			Kind: row.Kind, Method: row.Method, Route: row.Route, State: row.State, Message: row.Message,
			Evidence:        json.RawMessage(row.Evidence),
			FirstDetectedAt: row.FirstDetectedAt.Time.UTC().Format(time.RFC3339),
			LastDetectedAt:  row.LastDetectedAt.Time.UTC().Format(time.RFC3339),
		}
		if row.ResolvedAt.Valid {
			insight.ResolvedAt = row.ResolvedAt.Time.UTC().Format(time.RFC3339)
		}
		if len(insight.Evidence) == 0 {
			insight.Evidence = json.RawMessage("{}")
		}
		out = append(out, insight)
	}
	return out, nil
}
