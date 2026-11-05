package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) loadRealtimePublisherAdmin(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, state.RealtimePublisherCredentialStore, bool) {
	if !s.PreviewVisible(acct.ID) {
		httpkit.NoSuchRoute(w)
		return state.ManagedRealtimeEndpoint{}, nil, false
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return ep, nil, false
	}
	store, ok := s.Host.Store().(state.RealtimePublisherCredentialStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("publisher credentials require PostgreSQL storage"))
		return ep, nil, false
	}
	if id := r.PathValue("credential_id"); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			httpkit.NotFound(w, "publisher credential not found")
			return ep, nil, false
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	return ep, store, true
}
func (s *Service) RealtimePublisherCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimePublisherAdmin(w, r, acct)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := store.ListRealtimePublisherCredentials(r.Context(), ep.ID)
		if err != nil {
			s.writeRealtimePublisherError(w, r, err)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, rows)
		return
	}
	if !s.HistoryPreviewEnabled || !ep.Enabled {
		httpkit.NotFound(w, "publisher credential issuance unavailable")
		return
	}
	var req state.RealtimePublisherCredentialRequest
	if httpkit.DecodeJSONSized(r, &req, api.RealtimePublisherBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid publisher grant policy"))
		return
	}
	result, err := store.IssueRealtimePublisherCredential(r.Context(), ep.ID, req)
	if err != nil {
		s.writeRealtimePublisherError(w, r, err)
		return
	}
	s.auditRealtimePublisherCredential(r, acct, ep.ID, result.Info.ID, "issued")
	httpkit.WriteJSON(w, http.StatusCreated, result)
}
func (s *Service) RotateRealtimePublisherCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimePublisherAdmin(w, r, acct)
	if !ok {
		return
	}
	if !s.HistoryPreviewEnabled || !ep.Enabled {
		httpkit.NotFound(w, "publisher credential rotation unavailable")
		return
	}
	var req struct {
		TTLSeconds int `json:"ttl_seconds,omitempty"`
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimePublisherBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid rotation policy"))
		return
	}
	result, err := store.RotateRealtimePublisherCredential(r.Context(), ep.ID, r.PathValue("credential_id"), req.TTLSeconds)
	if err != nil {
		s.writeRealtimePublisherError(w, r, err)
		return
	}
	s.auditRealtimePublisherCredential(r, acct, ep.ID, result.Info.ID, "rotated")
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) RevokeRealtimePublisherCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimePublisherAdmin(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("credential_id")
	if err := store.RevokeRealtimePublisherCredential(r.Context(), ep.ID, id); err != nil {
		s.writeRealtimePublisherError(w, r, err)
		return
	}
	s.auditRealtimePublisherCredential(r, acct, ep.ID, id, "revoked")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) auditRealtimePublisherCredential(r *http.Request, acct state.Account, ep, id, action string) {
	s.Host.EmitAudit(r.Context(), "realtime.publisher_credential_"+action, &acct.ID, map[string]any{"endpoint_id": ep, "credential_id": id})
}
func (s *Service) writeRealtimePublisherError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, state.ErrRealtimePublisherCredentialDenied):
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Invalid publisher credential", "provide a current endpoint-bound publisher credential"))
	case errors.Is(err, state.ErrRealtimePublisherPermission):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Publish grant denied", "channel, event type or message TTL is outside this credential's grant, or realtime access is unavailable"))
	case errors.Is(err, state.ErrRealtimePublisherRate):
		w.Header().Set("Retry-After", "60")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "realtime_publisher_rate_limited", "Publisher rate limit reached", "accepted publishes exceeded this credential's per-minute limit; retry the same key after the window changes"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid grant, idempotency key, event type, metadata, TTL or payload size"))
	case errors.Is(err, state.ErrRealtimeConsumerGroupLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Publisher capacity reached", realtimeConsumerGroupCapacityDetail(err)))
	default:
		s.writeManagedRealtimeHistoryError(w, r, err)
	}
}
