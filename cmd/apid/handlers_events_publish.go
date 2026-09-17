package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// publishEvent handles POST /v1/events:publish. This first Workstream B slice
// is durable ingress only: matching and delivery are deliberately downstream
// consumers of the canonical event.published row.
func (s *server) publishEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PublishEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if req.AccountID != "" && req.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Forbidden", "account_id must match the authenticated account"))
		return
	}

	var occurredAt time.Time
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (events.Envelope{
		ID:              req.ID,
		Source:          req.Source,
		Type:            req.Type,
		Time:            occurredAt,
		DataContentType: req.DataContentType,
		Data:            req.Data,
		AccountID:       req.AccountID,
	}).Normalize(acct.ID, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		s.log.Error("marshal published event failed", "event_id", envelope.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	if err := s.store.AppendEvent(r.Context(), "apid", "event.published", &acct.ID, payload); err != nil {
		s.log.Error("record published event failed", "event_id", envelope.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}

	writeJSON(w, http.StatusAccepted, api.PublishEventResponse{
		ID:         envelope.ID,
		AcceptedAt: time.Now().UTC(),
		AccountID:  acct.ID,
	})
}
