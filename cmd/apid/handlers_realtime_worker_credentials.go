package main

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) loadRealtimeWorkerCredentialAdmin(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, state.RealtimeWorkerCredentialStore, bool) {
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return ep, nil, false
	}
	if problem := validateManagedRealtimeChannel(r.PathValue("channel")); problem != nil {
		api.WriteProblem(w, problem)
		return ep, nil, false
	}
	if !s.realtimeHistoryPreviewEnabled && r.PathValue("credential_id") == "" {
		s.notFound(w, "worker credentials preview unavailable")
		return ep, nil, false
	}
	store, ok := s.store.(state.RealtimeWorkerCredentialStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("worker credentials require durable PostgreSQL storage"))
		return ep, nil, false
	}
	if id := r.PathValue("credential_id"); id != "" {
		if _, err := uuid.Parse(id); err != nil {
			s.notFound(w, "credential not found")
			return ep, nil, false
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	return ep, store, true
}
func (s *server) realtimeWorkerCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeWorkerCredentialAdmin(w, r, acct)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := store.ListRealtimeWorkerCredentials(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
		if err != nil {
			s.writeRealtimeConsumerGroupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}
	if !s.realtimeHistoryPreviewEnabled || !ep.Enabled {
		s.notFound(w, "worker credential issuance unavailable")
		return
	}
	var req state.RealtimeWorkerCredentialRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker credential policy"))
		return
	}
	result, err := store.IssueRealtimeWorkerCredential(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeWorkerCredential(r, acct, ep.ID, result.Info.ID, "issued")
	writeJSON(w, http.StatusCreated, result)
}
func (s *server) rotateRealtimeWorkerCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeWorkerCredentialAdmin(w, r, acct)
	if !ok {
		return
	}
	if !s.realtimeHistoryPreviewEnabled || !ep.Enabled {
		s.notFound(w, "worker credential rotation unavailable")
		return
	}
	var req struct {
		TTLSeconds int `json:"ttl_seconds,omitempty"`
	}
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid credential rotation policy"))
		return
	}
	result, err := store.RotateRealtimeWorkerCredential(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), r.PathValue("credential_id"), req.TTLSeconds)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeWorkerCredential(r, acct, ep.ID, result.Info.ID, "rotated")
	writeJSON(w, http.StatusOK, result)
}
func (s *server) revokeRealtimeWorkerCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeWorkerCredentialAdmin(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("credential_id")
	if err := store.RevokeRealtimeWorkerCredential(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), id); err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeWorkerCredential(r, acct, ep.ID, id, "revoked")
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) auditRealtimeWorkerCredential(r *http.Request, acct state.Account, ep, id, action string) {
	s.audit.Emit(r.Context(), "realtime.worker_credential_"+action, &acct.ID, map[string]any{"endpoint_id": ep, "channel": r.PathValue("channel"), "group": r.PathValue("group"), "credential_id": id})
}
