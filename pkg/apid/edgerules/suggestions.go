package edgerules

// ADR-20261020105000: suggested protections — recipes (ADR-986) recommended from the
// app's own request telemetry over the last week, or the plan's telemetry
// retention when shorter. Read-only.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Bounds on the telemetry rows a suggestion pass reads.
const (
	recipeSuggestionUARows    = 32
	recipeSuggestionRouteRows = 100
)

// GetEdgeRuleSuggestions serves GET /v1/apps/{slug}/edge-rules/suggestions.
func (s *Service) GetEdgeRuleSuggestions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	days := api.EdgeRuleRecipeSuggestionDays(acct.Plan)
	if days == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := s.RecipeSuggestions(r.Context(), app, days)
	if err != nil {
		s.Host.Logger().Warn("edge-rule suggestions", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read request telemetry for suggestions"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// RecipeSuggestions ranks protection recipes for app from the last days of
// request telemetry and its current rules.
func (s *Service) RecipeSuggestions(ctx context.Context, app state.App, days int) (api.EdgeRuleRecipeSuggestionsResponse, error) {
	until := time.Now().UTC()
	since := until.AddDate(0, 0, -days)
	store := s.Host.Store()
	appID, accountID := pgUUID(app.ID), pgUUID(app.AccountID)
	from, to := pgtype.Timestamptz{Time: since, Valid: true}, pgtype.Timestamptz{Time: until, Valid: true}

	signals := api.EdgeRuleRecipeSuggestionSignals{Days: days}
	families, err := store.RequestTelemetryAnalyticsByDimension(ctx, sqlc.RequestTelemetryAnalyticsByDimensionParams{
		AppID: appID, AccountID: accountID, ReceivedAt: from, ReceivedAt_2: to, GroupBy: "ua_family", Limit: recipeSuggestionUARows,
	})
	if err != nil {
		return api.EdgeRuleRecipeSuggestionsResponse{}, fmt.Errorf("user-agent families: %w", err)
	}
	for _, row := range families {
		signals.TotalRequests += row.Requests
		if dimensionString(row.Dimension) == "bot" {
			signals.BotRequests += row.Requests
		}
	}
	routes, err := store.RequestTelemetryAnalyticsByRoute(ctx, sqlc.RequestTelemetryAnalyticsByRouteParams{
		AppID: appID, AccountID: accountID, ReceivedAt: from, ReceivedAt_2: to, Limit: recipeSuggestionRouteRows,
	})
	if err != nil {
		return api.EdgeRuleRecipeSuggestionsResponse{}, fmt.Errorf("routes: %w", err)
	}
	for _, row := range routes {
		method, path := api.SplitEdgeRuleRecipeSuggestionRoute(row.Route)
		if method == "" {
			method = row.Method
		}
		signals.Routes = append(signals.Routes, api.EdgeRuleRecipeSuggestionRoute{Method: method, Path: path, Requests: row.Requests, ErrorRequests: row.ErrorRequests})
	}
	rules, err := store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.EdgeRuleRecipeSuggestionsResponse{}, fmt.Errorf("rules: %w", err)
	}
	for _, rule := range rules {
		signals.Rules = append(signals.Rules, api.EdgeRuleRecipeSuggestionRule{Kind: string(rule.Kind), MatchPath: rule.MatchPath, Description: rule.Description, Enabled: rule.Enabled})
	}
	suggestions := api.SuggestEdgeRuleRecipes(signals)
	if suggestions == nil {
		suggestions = []api.EdgeRuleRecipeSuggestion{}
	}
	return api.EdgeRuleRecipeSuggestionsResponse{Window: fmt.Sprintf("%dd", days), Since: since, Suggestions: suggestions}, nil
}

func pgUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func dimensionString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}
