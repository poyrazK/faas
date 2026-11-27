package apidserver

// Operator console routes for stuck-customer signals and the customer's own
// health view (ADR-20261115175927). They are the JSON twins of the staff page
// at /dashboard/admin/support/attention and share its dismiss and contact
// code, so both front ends record the same outcomes and audit.
//
//	GET  /v1/admin/obs/attention/summary            — outcomes per kind, last 30 days
//	GET  /v1/admin/obs/attention/{id}/contact-draft — prefilled message for an open signal
//	POST /v1/admin/obs/attention/{id}/dismiss       — dismiss with an outcome
//	POST /v1/admin/obs/attention/{id}/contact       — open a ticket for the customer
//	GET  /v1/admin/obs/tenants/{id}/health          — what the customer sees on their health page
//
// Reads use the operator session read policy; writes use the admin mutation
// policy (operator allowlist, MFA, fresh step-up, same origin, Idempotency-Key)
// and must come from a signed-in session, as the staff page's do.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/attention"
	"github.com/onebox-faas/faas/pkg/state"
)

// attentionSignalFromPath loads the signal named by {id}, or writes a problem.
func (s *server) attentionSignalFromPath(w http.ResponseWriter, r *http.Request) (api.ObsAttentionSignal, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signal id", "expected a positive integer"))
		return api.ObsAttentionSignal{}, false
	}
	signal, err := s.attention.Get(r.Context(), id)
	if errors.Is(err, attention.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Signal not found", "no attention signal has this id"))
		return signal, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the signal"))
		return signal, false
	}
	return signal, true
}

// obsAttentionSummary handles GET /v1/admin/obs/attention/summary.
func (s *server) obsAttentionSummary(w http.ResponseWriter, r *http.Request, _ state.Account) {
	rows, err := s.attention.Summary(r.Context(), time.Now().Add(-api.AttentionSummaryWindow))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not summarise attention signals"))
		return
	}
	out := api.ObsAttentionSummaryResponse{WindowDays: int(api.AttentionSummaryWindow / (24 * time.Hour)), Items: make([]api.ObsAttentionKindSummary, 0, len(rows))}
	for _, k := range rows {
		out.Items = append(out.Items, api.ObsAttentionKindSummary{Kind: k.Kind, Total: k.Total, Open: k.Open, Contacted: k.Contacted,
			FalsePositive: k.FalsePositive, OtherDismissed: k.OtherDismissed, ResolvedOnOwn: k.ResolvedOnOwn, NoOutcome: k.NoOutcome,
			FalsePositivePercent: k.FalsePositivePercent(), MedianActSeconds: k.MedianActSeconds})
	}
	writeJSON(w, http.StatusOK, out)
}

// obsAttentionContactDraft handles GET /v1/admin/obs/attention/{id}/contact-draft.
func (s *server) obsAttentionContactDraft(w http.ResponseWriter, r *http.Request, _ state.Account) {
	signal, ok := s.attentionSignalFromPath(w, r)
	if !ok {
		return
	}
	if signal.State != api.AttentionStateOpen {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Signal not open", "This signal was already handled or resolved."))
		return
	}
	draft := newAttentionContact(signal)
	writeJSON(w, http.StatusOK, api.ObsAttentionContactDraft{Subject: draft.Subject, Category: draft.Category, Body: draft.Body,
		Categories: draft.Categories, SubjectMax: draft.SubjectMax, BodyMax: draft.BodyMax})
}

// postObsAttentionDismiss handles POST /v1/admin/obs/attention/{id}/dismiss.
func (s *server) postObsAttentionDismiss(w http.ResponseWriter, r *http.Request, staff state.Account) {
	var in api.ObsAttentionDismissRequest
	if err := decodeJSON(r, &in); err != nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"outcome\": ..., \"note\": ...}"))
		return
	}
	signal, ok := s.attentionSignalFromPath(w, r)
	if !ok {
		return
	}
	if problem := s.dismissAttention(r.Context(), signal.ID, staff.ID, in.Outcome, in.Note); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.writeAttentionAction(w, r, signal.ID, "")
}

// postObsAttentionContact handles POST /v1/admin/obs/attention/{id}/contact.
func (s *server) postObsAttentionContact(w http.ResponseWriter, r *http.Request, staff state.Account) {
	var in api.ObsAttentionContactRequest
	if err := decodeJSON(r, &in); err != nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"subject\": ..., \"category\": ..., \"body\": ...}"))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		api.WriteProblem(w, api.ErrValidation("an Idempotency-Key header is required"))
		return
	}
	signal, ok := s.attentionSignalFromPath(w, r)
	if !ok {
		return
	}
	ticketID, problem := s.contactAttention(r.Context(), signal, staff.ID, key, in)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.writeAttentionAction(w, r, signal.ID, ticketID)
}

// writeAttentionAction answers a write with the signal as it now stands.
func (s *server) writeAttentionAction(w http.ResponseWriter, r *http.Request, id int64, ticketID string) {
	signal, err := s.attention.Get(r.Context(), id)
	if err != nil {
		// The write succeeded; only the re-read failed.
		writeJSON(w, http.StatusOK, api.ObsAttentionActionResponse{Signal: api.ObsAttentionSignal{ID: id}, TicketID: ticketID})
		return
	}
	writeJSON(w, http.StatusOK, api.ObsAttentionActionResponse{Signal: signal, TicketID: ticketID})
}

// obsTenantHealth handles GET /v1/admin/obs/tenants/{id}/health: the
// account health page exactly as the customer sees it, so support can read
// what the customer reads before replying.
func (s *server) obsTenantHealth(w http.ResponseWriter, r *http.Request, _ state.Account) {
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "expected UUID"))
		return
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account has this id"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the account"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.accountHealthFor(r.Context(), r, target))
}
