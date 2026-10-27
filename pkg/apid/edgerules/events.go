package edgerules

// ADR-964: sampled edge-rule security events, newest first, for one app.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	edgeRuleEventsDefaultLimit = 50
	edgeRuleEventsMaxLimit     = 200
	// edgeRuleEventsMaxRequestID matches edge_rule_events_request_id_check.
	edgeRuleEventsMaxRequestID = 128
)

// parseEdgeRuleEventsSince accepts a Go duration ("90m", "24h") or whole
// days ("7d").
func parseEdgeRuleEventsSince(raw string) (time.Duration, error) {
	if raw == "" {
		return 24 * time.Hour, nil
	}
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(raw)
	}
	if err != nil || d <= 0 {
		return 0, errors.New("since must be a positive duration such as 1h, 24h or 7d")
	}
	return d, nil
}

// parseEdgeRuleEventsCursor decodes "<unix_nano>-<id>".
func parseEdgeRuleEventsCursor(raw string) (time.Time, int64, error) {
	nanos, id, ok := strings.Cut(raw, "-")
	n, errN := strconv.ParseInt(nanos, 10, 64)
	i, errI := strconv.ParseInt(id, 10, 64)
	if !ok || errN != nil || errI != nil {
		return time.Time{}, 0, errors.New("cursor is malformed")
	}
	return time.Unix(0, n).UTC(), i, nil
}

// EdgeRuleEventsQuery validates the query string against the plan window.
func EdgeRuleEventsQuery(r *http.Request, plan api.Plan, appID string, now time.Time) (state.EdgeRuleEventQuery, *api.Problem) {
	q := r.URL.Query()
	out := state.EdgeRuleEventQuery{AppID: appID, RuleID: q.Get("rule"), Outcome: q.Get("outcome"), RequestID: q.Get("request_id"), Limit: edgeRuleEventsDefaultLimit}
	if out.RuleID != "" {
		if _, err := uuid.Parse(out.RuleID); err != nil {
			return out, api.ErrValidation("rule must be an edge rule id")
		}
	}
	if len(out.RequestID) > edgeRuleEventsMaxRequestID {
		return out, api.ErrValidation(fmt.Sprintf("request_id must be at most %d bytes", edgeRuleEventsMaxRequestID))
	}
	if out.Outcome != "" && !state.IsEdgeRuleEventOutcome(out.Outcome) {
		return out, api.ErrValidation("outcome must be matched, logged, detected, warned, blocked, challenged, rejected, solved, failed or boxed")
	}
	since, err := parseEdgeRuleEventsSince(q.Get("since"))
	if err != nil {
		return out, api.ErrValidation(err.Error())
	}
	window := time.Duration(api.MustLimitsFor(plan).EdgeRuleEventsWindowHours) * time.Hour
	out.Since = now.Add(-min(since, window))
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > edgeRuleEventsMaxLimit {
			return out, api.ErrValidation(fmt.Sprintf("limit must be 1..%d", edgeRuleEventsMaxLimit))
		}
		out.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		if out.BeforeAt, out.BeforeID, err = parseEdgeRuleEventsCursor(raw); err != nil {
			return out, api.ErrValidation(err.Error())
		}
	}
	return out, nil
}

// GET /v1/apps/{slug}/edge-rules/events?rule=&outcome=&request_id=&since=&limit=&cursor=
func (s *Service) ListEdgeRuleEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EdgeRuleEventStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule events are unavailable"))
		return
	}
	query, prob := EdgeRuleEventsQuery(r, acct.Plan, app.ID, time.Now().UTC())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limit := query.Limit
	query.Limit++
	events, err := store.ListEdgeRuleEvents(r.Context(), query)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule events"))
		return
	}
	rules, err := s.Host.Store().ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rules"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, EdgeRuleEventsResponse(query.Since, events, rules, limit))
}

func EdgeRuleEventsResponse(since time.Time, events []state.EdgeRuleEvent, rules []state.EdgeRule, limit int) api.EdgeRuleEventsResponse {
	byID := make(map[string]state.EdgeRule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	out := api.EdgeRuleEventsResponse{Since: since, Events: make([]api.EdgeRuleEventResponse, 0, min(len(events), limit))}
	if len(events) > limit {
		last := events[limit-1]
		out.NextCursor = fmt.Sprintf("%d-%d", last.OccurredAt.UnixNano(), last.ID)
		events = events[:limit]
	}
	for _, e := range events {
		rule := byID[e.RuleID]
		out.Events = append(out.Events, api.EdgeRuleEventResponse{
			ID: strconv.FormatInt(e.ID, 10), RuleID: e.RuleID, RuleName: rule.Name, RuleKind: string(rule.Kind),
			Outcome: e.Outcome, OccurredAt: e.OccurredAt, RequestID: e.RequestID, Method: e.Method,
			Host: e.Host, Path: e.Path, ClientIP: e.ClientIP, Country: e.Country, UserAgent: e.UserAgent,
			WAFRuleIDs: e.WAFRuleIDs, WAFCategories: e.WAFCategories, WAFScore: e.WAFScore,
		})
	}
	return out
}
