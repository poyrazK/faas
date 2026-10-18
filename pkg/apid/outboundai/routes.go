package outboundai

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const routeInvalidDetail = "a route needs a DNS-label name, a format of native or openai, and 1 to 5 targets that are this account's enabled managed AI integrations: one ai_provider for native routes, or openai, azure_openai, anthropic and gemini targets (all but openai naming a model) for openai routes; a strategy of ordered, weighted, latency or cost, where a weighted route weighs every target from 0 to 100 (one above 0) and only a cost route takes max_ttfb_ms (1 to 600000)"

func (s *Service) routeStore(w http.ResponseWriter) (state.OutboundAIRouteStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIRouteStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
	}
	return store, ok
}

func routeResponse(route state.OutboundAIRoute) api.OutboundAIRoute {
	out := api.OutboundAIRoute{ID: route.ID, Name: route.Name, Format: route.Format, CreatedAt: route.CreatedAt, UpdatedAt: route.UpdatedAt,
		Targets: make([]api.OutboundAIRouteTarget, 0, len(route.Targets)), Strategy: route.Strategy, MaxTTFBMS: copyInt(route.MaxTTFBMS)}
	for _, target := range route.Targets {
		out.Targets = append(out.Targets, api.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Model: target.Model,
			Weight: copyInt(target.Weight)})
	}
	if route.Canary != nil {
		canary := canaryResponse(*route.Canary)
		out.Canary = &canary
	}
	return out
}

func stateRouteTargets(targets []api.OutboundAIRouteTarget) []state.OutboundAIRouteTarget {
	out := make([]state.OutboundAIRouteTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, state.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Model: target.Model, Weight: copyInt(target.Weight)})
	}
	return out
}

func (s *Service) ListRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	routes, err := store.ListOutboundAIRoutes(r.Context(), acct.ID)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
		return
	}
	out := api.OutboundAIRouteList{Items: make([]api.OutboundAIRoute, 0, len(routes))}
	for _, route := range routes {
		out.Items = append(out.Items, routeResponse(route))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// CreateRoute stores an ordered fallback route (ADR-990).
func (s *Service) CreateRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateOutboundAIRouteRequest
	if err := httpkit.DecodeJSONSized(r, &req, 8<<10); err != nil || !api.ValidOutboundAIRouteName(req.Name) ||
		!api.ValidOutboundAIRouteFormat(req.Format) || !api.ValidOutboundAIRouteTargetShape(req.Targets) ||
		!api.ValidOutboundAIRouteStrategy(req.Strategy, req.MaxTTFBMS, req.Targets) {
		problem(w, http.StatusBadRequest, api.CodeValidation, routeInvalidDetail)
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	created, err := store.CreateOutboundAIRoute(r.Context(), state.OutboundAIRoute{
		ID: uuid.NewString(), AccountID: acct.ID, Name: req.Name, Format: req.Format, Targets: stateRouteTargets(req.Targets),
		Strategy: req.Strategy, MaxTTFBMS: req.MaxTTFBMS,
	})
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, routeInvalidDetail)
	case errors.Is(err, state.ErrConflict):
		problem(w, http.StatusConflict, api.CodeConflict, "An AI route with this name already exists")
	case errors.Is(err, state.ErrOutboundAIRouteLimit):
		problem(w, http.StatusTooManyRequests, "outbound_ai_route_limit", "The account has reached its AI route limit")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI route could not be created")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusCreated, routeResponse(created))
	}
}

func routePathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	routeID := r.PathValue("route")
	if _, err := uuid.Parse(routeID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Route ID must be a UUID")
		return "", false
	}
	return routeID, true
}

func (s *Service) GetRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	route, err := store.GetOutboundAIRoute(r.Context(), acct.ID, routeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, routeResponse(route))
	}
}

func (s *Service) PutRouteTargets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIRouteTargetsRequest
	if err := httpkit.DecodeJSONSized(r, &req, 8<<10); err != nil || !api.ValidOutboundAIRouteTargetShape(req.Targets) ||
		!api.ValidOutboundAIRouteStrategy(req.Strategy, req.MaxTTFBMS, req.Targets) {
		problem(w, http.StatusBadRequest, api.CodeValidation, routeInvalidDetail)
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIRouteTargets(r.Context(), acct.ID, routeID, stateRouteTargets(req.Targets),
		state.OutboundAIRouteRouting{Strategy: req.Strategy, MaxTTFBMS: req.MaxTTFBMS})
	s.writeResult(w, err, "AI route not found", routeInvalidDetail)
}

// GetRouteStats returns each target's attempts over the window latency and
// cost routes judge (ADR-1434).
func (s *Service) GetRouteStats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	window, err := store.OutboundAIRouteStats(r.Context(), acct.ID, routeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI route stats are unavailable")
	default:
		out := api.OutboundAIRouteStats{Strategy: window.Strategy, WindowStart: window.WindowStart,
			Targets: make([]api.OutboundAIRouteTargetStats, 0, len(window.Targets))}
		for _, stats := range window.Targets {
			out.Targets = append(out.Targets, targetStatsResponse(stats))
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func (s *Service) DeleteRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.routeStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundAIRoute(r.Context(), acct.ID, routeID)
	s.writeResult(w, err, "AI route not found", routeInvalidDetail)
}
