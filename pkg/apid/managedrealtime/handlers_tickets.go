package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) IssueRealtimeTicket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.TicketsPreviewEnabled {
		httpkit.NotFound(w, "subscription tickets preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeSubscriptionTicketStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("ticket store unavailable"))
		return
	}
	var req api.RealtimeTicketRequest
	if httpkit.DecodeJSONSized(r, &req, 16384) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid ticket request"))
		return
	}
	token, claims, err := store.IssueRealtimeSubscriptionTicket(r.Context(), ep.ID, req.Principal, req.Channels, req.TTLSeconds)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrRealtimeInvalid("ticket requires a principal, 1-32 unique channels and ttl_seconds 1-300"))
	case errors.Is(err, state.ErrManagedRealtimeHistoryLimit):
		api.WriteProblem(w, api.ErrRealtimeInvalid("ticket capacity reached").WithLimit(api.RealtimeTicketMaxPerEndpoint, api.RealtimeTicketMaxPerEndpoint+1))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "enabled endpoint required")
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not issue ticket"))
	default:
		s.Host.EmitAudit(r.Context(), "realtime.ticket_issued", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channels": len(req.Channels), "ttl_seconds": req.TTLSeconds})
		httpkit.WriteJSON(w, http.StatusCreated, api.RealtimeTicketResponse{Ticket: token, ExpiresAt: claims.ExpiresAt})
	}
}
