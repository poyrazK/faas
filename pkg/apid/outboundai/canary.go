package outboundai

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const canaryInvalidDetail = "a canary needs a target from 2 to the route's last target, a percent from 1 to 50, " +
	"max_error_rate_increase from 0 to 100, max_latency_increase_percent from 1 to 1000 or null, " +
	"min_calls from 10 to 100,000 and window_minutes from 5 to 1440"

func canaryResponse(canary state.OutboundAIRouteCanary) api.OutboundAIRouteCanary {
	out := api.OutboundAIRouteCanary{
		Target: canary.Position, Percent: canary.Percent, MaxErrorRateIncrease: canary.MaxErrorRateIncrease,
		MaxLatencyIncreasePercent: canary.MaxLatencyIncreasePercent, MinCalls: canary.MinCalls,
		WindowMinutes: canary.WindowMinutes, State: canary.State, StartedAt: canary.StartedAt, DecidedAt: canary.DecidedAt,
	}
	if canary.Reason != "" {
		reason := canary.Reason
		out.Reason = &reason
	}
	return out
}

func targetStatsResponse(stats state.OutboundAIRouteTargetStats) api.OutboundAIRouteTargetStats {
	out := api.OutboundAIRouteTargetStats{Target: stats.Position, Calls: stats.Calls, Errors: stats.Errors,
		CostMillicents: stats.CostMillicents}
	if stats.Calls > 0 {
		out.ErrorRate = float64(stats.Errors) / float64(stats.Calls)
	}
	if stats.TTFBCalls > 0 {
		mean := stats.TTFBMSTotal / stats.TTFBCalls
		out.MeanTTFBMS = &mean
	}
	if successes := stats.Calls - stats.Errors; successes > 0 {
		mean := stats.CostMillicents / successes
		out.MeanCostMillicents = &mean
	}
	return out
}

// PutRouteCanary starts or replaces a route's model canary (ADR-1340).
func (s *Service) PutRouteCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIRouteCanaryRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, canaryInvalidDetail)
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	req = req.Normalized()
	canary, err := store.SetOutboundAIRouteCanary(r.Context(), acct.ID, routeID, state.OutboundAIRouteCanary{
		Position: req.Target, Percent: req.Percent, MaxErrorRateIncrease: *req.MaxErrorRateIncrease,
		MaxLatencyIncreasePercent: req.MaxLatencyIncreasePercent, MinCalls: *req.MinCalls, WindowMinutes: *req.WindowMinutes,
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, canaryInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "The AI route canary could not be started")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, canaryResponse(canary))
	}
}

// GetRouteCanary returns a route's canary with the first target's and the
// canary target's counts over its window.
func (s *Service) GetRouteCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	status, err := store.OutboundAIRouteCanaryStatus(r.Context(), acct.ID, routeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route canary not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.OutboundAIRouteCanaryStatus{
			Canary: canaryResponse(status.Canary), WindowStart: status.WindowStart,
			Baseline: targetStatsResponse(status.Baseline), CanaryStats: targetStatsResponse(status.CanaryStats),
		})
	}
}

// PromoteRouteCanary makes the canary target the route's first target and
// ends the canary.
func (s *Service) PromoteRouteCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	route, err := store.PromoteOutboundAIRouteCanary(r.Context(), acct.ID, routeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route canary not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusConflict, api.CodeConflict, "The canary no longer names one of the route's targets")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "The AI route canary could not be promoted")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, routeResponse(route))
	}
}

// DeleteRouteCanary ends a route's canary.
func (s *Service) DeleteRouteCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundAIRouteCanary(r.Context(), acct.ID, routeID)
	s.writeResult(w, err, "AI route canary not found", canaryInvalidDetail)
}
