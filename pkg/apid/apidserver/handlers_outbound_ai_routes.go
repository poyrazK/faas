package apidserver

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const outboundAIRouteInvalidDetail = "a route needs a DNS-label name, a format of native or openai, and 1 to 5 targets that are this account's enabled managed AI integrations: one ai_provider for native routes, or openai, azure_openai, anthropic and gemini targets (all but openai naming a model) for openai routes"

func (s *server) outboundAIRouteStore(w http.ResponseWriter) (state.OutboundAIRouteStore, bool) {
	store, ok := s.store.(state.OutboundAIRouteStore)
	if !ok {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
	}
	return store, ok
}

func outboundAIRouteResponse(route state.OutboundAIRoute) api.OutboundAIRoute {
	out := api.OutboundAIRoute{ID: route.ID, Name: route.Name, Format: route.Format, CreatedAt: route.CreatedAt, UpdatedAt: route.UpdatedAt,
		Targets: make([]api.OutboundAIRouteTarget, 0, len(route.Targets))}
	for _, target := range route.Targets {
		out.Targets = append(out.Targets, api.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Model: target.Model})
	}
	return out
}

func stateOutboundAIRouteTargets(targets []api.OutboundAIRouteTarget) []state.OutboundAIRouteTarget {
	out := make([]state.OutboundAIRouteTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, state.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Model: target.Model})
	}
	return out
}

func (s *server) listOutboundAIRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.outboundAIRouteStore(w)
	if !ok {
		return
	}
	routes, err := store.ListOutboundAIRoutes(r.Context(), acct.ID)
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
		return
	}
	out := api.OutboundAIRouteList{Items: make([]api.OutboundAIRoute, 0, len(routes))}
	for _, route := range routes {
		out.Items = append(out.Items, outboundAIRouteResponse(route))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// createOutboundAIRoute stores an ordered fallback route (ADR-990).
func (s *server) createOutboundAIRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateOutboundAIRouteRequest
	if err := decodeJSONSized(r, &req, 8<<10); err != nil || !api.ValidOutboundAIRouteName(req.Name) ||
		!api.ValidOutboundAIRouteFormat(req.Format) || !api.ValidOutboundAIRouteTargetShape(req.Targets) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIRouteInvalidDetail)
		return
	}
	store, ok := s.outboundAIRouteStore(w)
	if !ok {
		return
	}
	created, err := store.CreateOutboundAIRoute(r.Context(), state.OutboundAIRoute{
		ID: uuid.NewString(), AccountID: acct.ID, Name: req.Name, Format: req.Format, Targets: stateOutboundAIRouteTargets(req.Targets),
	})
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIRouteInvalidDetail)
	case errors.Is(err, state.ErrConflict):
		outboundBindingProblem(w, http.StatusConflict, api.CodeConflict, "An AI route with this name already exists")
	case errors.Is(err, state.ErrOutboundAIRouteLimit):
		outboundBindingProblem(w, http.StatusTooManyRequests, "outbound_ai_route_limit", "The account has reached its AI route limit")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI route could not be created")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, outboundAIRouteResponse(created))
	}
}

func outboundAIRoutePathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	routeID := r.PathValue("route")
	if _, err := uuid.Parse(routeID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Route ID must be a UUID")
		return "", false
	}
	return routeID, true
}

func (s *server) getOutboundAIRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := outboundAIRoutePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.outboundAIRouteStore(w)
	if !ok {
		return
	}
	route, err := store.GetOutboundAIRoute(r.Context(), acct.ID, routeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "AI route not found")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_routes_unavailable", "Outbound AI routes are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, outboundAIRouteResponse(route))
	}
}

func (s *server) putOutboundAIRouteTargets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := outboundAIRoutePathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIRouteTargetsRequest
	if err := decodeJSONSized(r, &req, 8<<10); err != nil || !api.ValidOutboundAIRouteTargetShape(req.Targets) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIRouteInvalidDetail)
		return
	}
	store, ok := s.outboundAIRouteStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIRouteTargets(r.Context(), acct.ID, routeID, stateOutboundAIRouteTargets(req.Targets))
	s.writeOutboundAIWriteResult(w, err, "AI route not found", outboundAIRouteInvalidDetail)
}

func (s *server) deleteOutboundAIRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := outboundAIRoutePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.outboundAIRouteStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundAIRoute(r.Context(), acct.ID, routeID)
	s.writeOutboundAIWriteResult(w, err, "AI route not found", outboundAIRouteInvalidDetail)
}
