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
	s.Host.Audit(r.Context(), "billing_run_schedule.updated", acct.ID, map[string]any{"delay_hours": saved.DelayHours,
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
	delay := api.BillingRunScheduleDefaultDelayHours
	if req.DelayHours != nil {
		delay = *req.DelayHours
	}
	if delay < api.BillingRunScheduleMinDelayHours || delay > api.BillingRunScheduleMaxDelayHours {
		return state.BillingRunSchedule{}, validationProblem("Invalid delay",
			fmt.Sprintf("delay_hours must be %d to %d", api.BillingRunScheduleMinDelayHours, api.BillingRunScheduleMaxDelayHours)).
			WithLimit(api.BillingRunScheduleMaxDelayHours, int64(delay))
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
