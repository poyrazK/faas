package billingruns

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// scheduleRunAt is when a schedule starts the run for periodStart's month.
func scheduleRunAt(periodStart time.Time, delayHours int) time.Time {
	return periodStart.AddDate(0, 1, 0).Add(time.Duration(delayHours) * time.Hour)
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// PutSchedule creates or replaces the account's schedule. A new schedule
// starts with the current month; a replaced one keeps its next month.
func (s *Service) PutSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	var req api.PutBillingRunScheduleRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	sched, problem := s.requestedSchedule(r, acct, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	saved, err := store.PutBillingRunSchedule(r.Context(), sched)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the billing run schedule"))
		return
	}
	s.writeSavedSchedule(w, r, saved)
}

func (s *Service) writeSavedSchedule(w http.ResponseWriter, r *http.Request, saved state.BillingRunSchedule) {
	s.Host.Audit(r.Context(), "billing_run_schedule.updated", saved.AccountID, map[string]any{"delay_hours": saved.DelayHours,
		"export": saved.Export, "next_month": saved.NextPeriodStart.Format("2006-01")})
	httpkit.WriteJSON(w, http.StatusOK, scheduleResponse(saved))
}

func (s *Service) requestedSchedule(r *http.Request, acct state.Account, store state.BillingRunStore,
	req api.PutBillingRunScheduleRequest) (state.BillingRunSchedule, *api.Problem) {
	opts, problem := runOptions(req.Sources, req.Finalize, req.Export, req.HoldOnGaps)
	if problem != nil {
		return state.BillingRunSchedule{}, problem
	}
	if problem := s.requireProductControl(opts); problem != nil {
		return state.BillingRunSchedule{}, problem
	}
	delay, problem := scheduleDelay(req.DelayHours)
	if problem != nil {
		return state.BillingRunSchedule{}, problem
	}
	if problem := s.requireExportTarget(r.Context(), acct, opts); problem != nil {
		return state.BillingRunSchedule{}, problem
	}
	next := monthStart(s.now())
	if existing, err := store.GetBillingRunSchedule(r.Context(), acct.ID); err == nil {
		next = existing.NextPeriodStart
	} else if !errors.Is(err, state.ErrNotFound) {
		return state.BillingRunSchedule{}, api.ErrInternal("could not load the billing run schedule")
	}
	return state.BillingRunSchedule{AccountID: acct.ID, BillingRunOptions: opts, DelayHours: delay,
		NextPeriodStart: next, NextRunAt: scheduleRunAt(next, delay)}, nil
}

func scheduleDelay(requested *int) (int, *api.Problem) {
	delay := api.BillingRunScheduleDefaultDelayHours
	if requested != nil {
		delay = *requested
	}
	if delay < api.BillingRunScheduleMinDelayHours || delay > api.BillingRunScheduleMaxDelayHours {
		return 0, validationProblem("Invalid delay",
			fmt.Sprintf("delay_hours must be %d to %d", api.BillingRunScheduleMinDelayHours, api.BillingRunScheduleMaxDelayHours)).
			WithLimit(api.BillingRunScheduleMaxDelayHours, int64(delay))
	}
	return delay, nil
}

func scheduleChanged() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Billing schedule changed",
		"Reload the schedule and review its current settings before trying again.")
}

// PutDraftSchedule creates a draft-only schedule, or updates the timing
// of the draft schedule the operator reviewed.
func (s *Service) PutDraftSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	var req api.PutDraftBillingRunScheduleRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	delay, problem := scheduleDelay(req.DelayHours)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	finalize := false
	opts, problem := runOptions(nil, &finalize, state.BillingRunExportNone, nil)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	sched := state.BillingRunSchedule{
		AccountID: acct.ID, BillingRunOptions: opts, NextPeriodStart: monthStart(s.now()),
	}
	var expected time.Time
	if req.ExpectedUpdatedAt != nil {
		expected = *req.ExpectedUpdatedAt
		if expected.IsZero() {
			api.WriteProblem(w, badScheduleVersion())
			return
		}
		current, err := store.GetBillingRunSchedule(r.Context(), acct.ID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, scheduleChanged())
			return
		case err != nil:
			api.WriteProblem(w, api.ErrInternal("could not load the billing run schedule"))
			return
		case !current.DraftOnly() || !current.UpdatedAt.Equal(expected):
			api.WriteProblem(w, scheduleChanged())
			return
		}
		sched = current
	}
	sched.DelayHours, sched.NextRunAt = delay, scheduleRunAt(sched.NextPeriodStart, delay)
	saved, err := store.PutDraftBillingRunSchedule(r.Context(), sched, expected)
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, scheduleChanged())
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not save the billing run schedule"))
	default:
		s.writeSavedSchedule(w, r, saved)
	}
}

func badScheduleVersion() *api.Problem {
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid schedule version",
		"expected_updated_at must be the schedule's updated_at timestamp")
}

// DeleteDraftSchedule stops future runs of the reviewed draft version.
func (s *Service) DeleteDraftSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	expected, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("expected_updated_at"))
	if err != nil || expected.IsZero() {
		api.WriteProblem(w, badScheduleVersion())
		return
	}
	switch err := store.DeleteDraftBillingRunSchedule(r.Context(), acct.ID, expected); {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, scheduleChanged())
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not delete the billing run schedule"))
	default:
		s.Host.Audit(r.Context(), "billing_run_schedule.deleted", acct.ID, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetSchedule returns the account's schedule.
func (s *Service) GetSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	sched, err := store.GetBillingRunSchedule(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no billing run schedule")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the billing run schedule"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, scheduleResponse(sched))
}

// DeleteSchedule stops scheduled runs; a run already started continues.
func (s *Service) DeleteSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	err := store.DeleteBillingRunSchedule(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no billing run schedule")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not delete the billing run schedule"))
		return
	}
	s.Host.Audit(r.Context(), "billing_run_schedule.deleted", acct.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func scheduleResponse(sched state.BillingRunSchedule) api.BillingRunScheduleResponse {
	out := api.BillingRunScheduleResponse{Sources: slices.Clone(sched.Sources), Finalize: sched.Finalize, Export: sched.Export,
		HoldOnGaps: sched.HoldOnGaps, DelayHours: sched.DelayHours, NextMonth: sched.NextPeriodStart.UTC().Format("2006-01"),
		NextRunAt: sched.NextRunAt.UTC(), CreatedAt: sched.CreatedAt.UTC(), UpdatedAt: sched.UpdatedAt.UTC()}
	if sched.LastPeriodStart != nil {
		out.LastMonth = sched.LastPeriodStart.UTC().Format("2006-01")
	}
	return out
}
