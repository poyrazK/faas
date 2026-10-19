package internalmail

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) relayRouteDisabled(path string) bool {
	return !s.backpressureEnabled() && (path == RelayHealthPath || path == RelayOperationsPath || strings.HasPrefix(path, RelayOperationsPath+"/"))
}

func (s *Service) registerRelayHealth(mux *http.ServeMux) {
	mux.HandleFunc("POST "+RelayHealthPath, s.postRelayHealth)
	mux.HandleFunc("GET "+RelayOperationsPath, s.getRelayHealth)
	mux.HandleFunc("GET "+RelayOperationsPath+"/events", s.getRelayEvents)
}

func (s *Service) postRelayHealth(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID            string    `json:"id"`
		ObservedAt    time.Time `json:"observed_at"`
		InventoryOK   *bool     `json:"inventory_ok"`
		SMTPUp        *bool     `json:"smtp_up"`
		QueueMessages *int64    `json:"queue_messages"`
		OldestSeconds *int64    `json:"oldest_seconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.InternalMailMaxRelayHealthBytes)
	if !decode(w, r, &input) {
		return
	}
	if input.InventoryOK == nil || input.SMTPUp == nil || input.QueueMessages == nil || input.OldestSeconds == nil {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := s.ReportRelayHealth(r.Context(), RelayObservation{ID: input.ID, ObservedAt: input.ObservedAt, InventoryOK: *input.InventoryOK, SMTPUp: *input.SMTPUp, QueueMessages: *input.QueueMessages, OldestSeconds: *input.OldestSeconds})
	respond(w, status, err, http.StatusOK)
}

func (s *Service) getRelayHealth(w http.ResponseWriter, r *http.Request) {
	status, err := s.RelayStatus(r.Context())
	respond(w, status, err, http.StatusOK)
}

func (s *Service) getRelayEvents(w http.ResponseWriter, r *http.Request) {
	if !s.operationsEnabled() || !s.backpressureEnabled() {
		respond(w, nil, ErrDisabled, 0)
		return
	}
	store, ok := s.Store.(backpressureStore)
	if !ok {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	list, err := operatorList(r, true, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := store.relayEvents(r.Context(), list)
	respond(w, page, err, http.StatusOK)
}
