package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) IssueRealtimeTicket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.previewFlagUnavailable(w, r, acct, platformflags.RealtimeTickets, s.TicketsPreviewEnabled, "subscription tickets preview unavailable") {
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
		httpkit.WriteJSON(w, http.StatusCreated, api.RealtimeTicketResponse{Ticket: token, TicketID: claims.ID, ExpiresAt: claims.ExpiresAt})
	}
}

// ticketRevoker loads the endpoint and the store for a revocation, or answers
// the request itself and returns ok=false.
func (s *Service) ticketRevoker(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, state.RealtimeSubscriptionTicketRevoker, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.previewFlagUnavailable(w, r, acct, platformflags.RealtimeTickets, s.TicketsPreviewEnabled, "subscription tickets preview unavailable") {
		return state.ManagedRealtimeEndpoint{}, nil, false
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return ep, nil, false
	}
	store, ok := s.Host.Store().(state.RealtimeSubscriptionTicketRevoker)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("ticket store unavailable"))
		return ep, nil, false
	}
	return ep, store, true
}

// RevokeRealtimeTicket ends one ticket. Its sockets and SSE streams close
// within about a second, when they next re-read the ticket.
func (s *Service) RevokeRealtimeTicket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.ticketRevoker(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("ticket")
	switch err := store.RevokeRealtimeSubscriptionTicket(r.Context(), ep.ID, id); {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrRealtimeInvalid("ticket_id must be the lowercase UUID returned when the ticket was issued"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "ticket not found or already expired")
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not revoke ticket"))
	default:
		s.Host.EmitAudit(r.Context(), "realtime.ticket_revoked", &acct.ID, map[string]any{"endpoint_id": ep.ID, "ticket_id": id})
		w.WriteHeader(http.StatusNoContent)
	}
}

// RevokeRealtimeTickets ends every live ticket of one principal, for example
// at logout.
func (s *Service) RevokeRealtimeTickets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.ticketRevoker(w, r, acct)
	if !ok {
		return
	}
	principals := r.URL.Query()["principal"]
	if len(principals) != 1 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("exactly one principal query parameter is required"))
		return
	}
	revoked, err := store.RevokeRealtimeSubscriptionTicketsForPrincipal(r.Context(), ep.ID, principals[0])
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrRealtimeInvalid("principal must be non-empty, at most 256 bytes, and contain no NUL or newline"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not revoke tickets"))
	default:
		s.Host.EmitAudit(r.Context(), "realtime.tickets_revoked", &acct.ID, map[string]any{"endpoint_id": ep.ID, "revoked": revoked})
		httpkit.WriteJSON(w, http.StatusOK, api.RealtimeTicketsRevokeResponse{Revoked: revoked})
	}
}
