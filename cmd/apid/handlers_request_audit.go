package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppRequestAudit exposes verified, exact gateway observations. It is not
// an application business-event log: neither user identity nor internal or
// outbound dependency calls can be inferred from every HTTP request.
func (s *server) getAppRequestAudit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.RequestAuditStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("request audit"))
		return
	}
	until := time.Now().UTC().Add(time.Nanosecond)
	query := r.URL.Query()
	if raw := query.Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("until must be an RFC3339 timestamp"))
			return
		}
		until = parsed.UTC()
	}
	since := until.Add(-24 * time.Hour)
	if raw := query.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("since must be an RFC3339 timestamp"))
			return
		}
		since = parsed.UTC()
	}
	if !until.After(since) || until.Sub(since) > 31*24*time.Hour {
		api.WriteProblem(w, api.ErrValidation("audit window must be positive and at most 31 days"))
		return
	}
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			api.WriteProblem(w, api.ErrValidation("limit must be between 1 and 500"))
			return
		}
		limit = parsed
	}
	records, err := store.ListRequestAudit(r.Context(), acct.ID, app.ID, since, until, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("request audit"))
		return
	}
	responseRecords := make([]api.RequestAuditRecord, 0, len(records))
	for _, record := range records {
		responseRecords = append(responseRecords, api.RequestAuditRecord{
			EventID: record.EventID, AccountID: record.AccountID, AppID: record.AppID,
			ConsumerID: record.ConsumerID, PlatformTenantID: record.PlatformTenantID,
			RouteTemplate: record.RouteTemplate, Method: record.Method,
			HTTPStatus: record.HTTPStatus, LatencyMS: record.LatencyMS,
			TraceID: record.TraceID, DeploymentID: record.DeploymentID,
			CommitSHA: record.CommitSHA, OccurredAt: record.OccurredAt,
			RequestID: record.RequestID, SourceIP: record.SourceIP,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_id": app.ID, "records": responseRecords, "since": since, "until": until})
}

// getAppDiscoveredRoutes reads the independent, capped API inventory. Exact
// audit collection can remain off; unknown literal slugs are still a privacy
// risk, so discovery requires its own operator opt-in.
func (s *server) getAppDiscoveredRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.APIRouteInventoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("discovered routes"))
		return
	}
	routes, capHit, err := store.ListDiscoveredAPIRoutes(r.Context(), acct.ID, app.ID, state.DiscoveredRouteLimit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("discovered routes"))
		return
	}
	responseRoutes := make([]api.DiscoveredAPIRoute, 0, len(routes))
	for _, route := range routes {
		responseRoutes = append(responseRoutes, api.DiscoveredAPIRoute{
			RouteTemplate: route.RouteTemplate, FirstSeen: route.FirstSeen,
			LastSeen: route.LastSeen, RequestCount: route.RequestCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"app_id": app.ID, "routes": responseRoutes, "cap_hit": capHit, "source": "usage_outbox"})
}
