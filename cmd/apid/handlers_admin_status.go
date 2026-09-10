package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createAdminStatusEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	var request api.AdminStatusEventCreateRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad JSON", err.Error()))
		return
	}
	kind := publicstatus.Kind(request.Kind)
	lifecycle := publicstatus.Lifecycle(request.State)
	if lifecycle == "" {
		if kind == publicstatus.KindMaintenance {
			lifecycle = publicstatus.LifecycleScheduled
		} else {
			lifecycle = publicstatus.LifecycleInvestigating
		}
	}
	impact := publicstatus.State(request.Impact)
	if impact == "" && kind == publicstatus.KindMaintenance {
		impact = publicstatus.StateMaintenance
	}
	startsAt := request.StartsAt
	if kind == publicstatus.KindIncident && startsAt == nil {
		now := time.Now().UTC()
		startsAt = &now
	}
	components := make([]publicstatus.Component, len(request.Components))
	for i, component := range request.Components {
		components[i] = publicstatus.Component(component)
	}
	event, err := s.store.CreatePublicStatusEvent(r.Context(), state.StatusEventCreate{
		IdempotencyKey: acct.ID + ":" + strings.TrimSpace(r.Header.Get("Idempotency-Key")), Actor: acct.ID,
		Kind: kind, Title: request.Title, Impact: impact, Components: components, State: lifecycle,
		StartsAt: startsAt, ScheduledStartAt: request.ScheduledStartAt, ScheduledEndAt: request.ScheduledEndAt,
		Message: request.Message,
	})
	if err != nil {
		s.statusMetrics.observeMutation(string(kind), "create", statusMutationOutcome(err))
	}
	if writeStatusMutationError(w, err) {
		return
	}
	s.statusMetrics.observeMutation(string(kind), "create", "ok")
	if s.statusCache != nil {
		s.statusCache.invalidatePublic()
	}
	writeJSON(w, http.StatusCreated, publicStatusEvent(event))
}

func (s *server) updateAdminStatusEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	publicID := r.PathValue("public_id")
	if _, err := uuid.Parse(publicID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad event id", "expected a public UUID"))
		return
	}
	var request api.AdminStatusEventUpdateRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad JSON", err.Error()))
		return
	}
	prior, err := s.store.StatusEventByPublicID(r.Context(), publicID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Status event not found", "No public event exists with that id."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load status event"))
		return
	}
	event, err := s.store.AppendPublicStatusUpdate(r.Context(), publicID, state.StatusEventUpdateInput{
		IdempotencyKey: acct.ID + ":" + strings.TrimSpace(r.Header.Get("Idempotency-Key")), Actor: acct.ID,
		State: publicstatus.Lifecycle(request.State), Message: request.Message,
	})
	if err != nil {
		s.statusMetrics.observeMutation(string(prior.Kind), "update", statusMutationOutcome(err))
	}
	if writeStatusMutationError(w, err) {
		return
	}
	s.statusMetrics.observeMutation(string(prior.Kind), "update", "ok")
	if s.statusCache != nil {
		s.statusCache.invalidatePublic()
	}
	writeJSON(w, http.StatusOK, publicStatusEvent(event))
}

func statusMutationOutcome(err error) string {
	var validation *publicstatus.ValidationError
	if errors.As(err, &validation) || errors.Is(err, state.ErrNotFound) {
		return "rejected"
	}
	return "error"
}

func (s *server) listAdminStatusEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	events, err := s.store.ListPublicStatusEvents(r.Context(), state.StatusEventListOptions{
		Kind: publicstatus.Kind(r.URL.Query().Get("kind")), ActiveOnly: r.URL.Query().Get("active") == "true", Limit: 200,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list status events"))
		return
	}
	writeJSON(w, http.StatusOK, publicStatusEvents(events))
}

func writeStatusMutationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var validation *publicstatus.ValidationError
	if errors.As(err, &validation) {
		statusCode := http.StatusBadRequest
		if validation.Code == publicstatus.CodeInvalidTransition || validation.Code == publicstatus.CodeTerminalEvent || validation.Code == publicstatus.CodeIdempotencyConflict {
			statusCode = http.StatusConflict
		}
		api.WriteProblem(w, api.NewProblem(statusCode, validation.Code, "Status event rejected", validation.Message))
		return true
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Status event not found", "No public event exists with that id."))
		return true
	}
	api.WriteProblem(w, api.ErrCapacity("could not mutate status event"))
	return true
}
