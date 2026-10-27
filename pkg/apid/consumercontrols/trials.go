// Package consumercontrols serves API consumer and platform tenant billing
// controls: free trials (ADR-1314, ADR-1322) and blocking consumers and
// tenants whose invoices are overdue (ADR-1315, ADR-1324). Like the other
// apid domains (ADR-982), it reaches apid only through Host; apidserver
// keeps the route table.
package consumercontrols

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what consumer billing controls need from apid.
type Host interface {
	Store() state.Store
	// ConsumerRoute resolves the app and consumer of a consumer route; on
	// false it has written the problem.
	ConsumerRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, bool)
	// AppRoute resolves the {slug} app; on false it has written the problem.
	AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool)
	// TenantRoute resolves the {id} platform tenant; on false it has
	// written the problem.
	TenantRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, bool)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
}

// Service serves trials and overdue blocking and runs the overdue worker.
// Now is the clock; nil uses time.Now.
type Service struct {
	Host Host
	Now  func() time.Time
	// Enabled pauses the background worker while it reports false: API
	// monetization is released by a runtime setting (ADR-20261018090210).
	// Nil runs it.
	Enabled func() bool
	// Released reports whether API monetization is released to an account
	// (ADR-20261020170649); the worker skips accounts it is not. Nil
	// releases every account.
	Released func(accountID string) bool
}

// released reports whether the worker serves an account's monetization.
func (s *Service) released(accountID string) bool { return s.Released == nil || s.Released(accountID) }

// tick runs one background tick unless Enabled pauses the worker; paused,
// holds are neither placed nor lifted.
func (s *Service) tick(ctx context.Context) error {
	if s.Enabled != nil && !s.Enabled() {
		return nil
	}
	return s.SyncOverdueOnce(ctx)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// nextMinute is the first whole UTC minute after now: changes to a trial
// never reach a minute that may already be billed.
func (s *Service) nextMinute() time.Time {
	return s.now().Truncate(time.Minute).Add(time.Minute)
}

func validation(title, detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, title, detail)
}

func (s *Service) trialStore(w http.ResponseWriter) (state.APIConsumerTrialStore, bool) {
	store, ok := s.Host.Store().(state.APIConsumerTrialStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer trials are unavailable"))
	}
	return store, ok
}

func (s *Service) trialResponse(t state.APIConsumerTrial) api.APIConsumerTrialResponse {
	return api.APIConsumerTrialResponse{ConsumerID: t.ConsumerID, StartsAt: t.StartsAt, EndsAt: t.EndsAt, Active: t.EndsAt.After(s.now())}
}

// StartTrial gives a new consumer a trial of days from the next minute.
// Consumer creation calls it.
func (s *Service) StartTrial(ctx context.Context, acct state.Account, consumer state.APIConsumer, days int) (state.APIConsumerTrial, error) {
	store, ok := s.Host.Store().(state.APIConsumerTrialStore)
	if !ok {
		return state.APIConsumerTrial{}, errors.New("consumer trials are unavailable")
	}
	start := s.nextMinute()
	trial, err := store.PutAPIConsumerTrial(ctx, state.APIConsumerTrial{AccountID: acct.ID, AppID: consumer.AppID,
		ConsumerID: consumer.ID, StartsAt: start, EndsAt: start.AddDate(0, 0, days)})
	if err != nil {
		return state.APIConsumerTrial{}, err
	}
	s.Host.Audit(ctx, "api_consumer.trial_set", acct.ID, map[string]any{"app_id": consumer.AppID, "consumer_id": consumer.ID,
		"starts_at": trial.StartsAt, "ends_at": trial.EndsAt})
	return trial, nil
}

// ValidTrialDays reports whether a requested trial length is allowed.
func ValidTrialDays(days int) bool { return days >= 1 && days <= state.MaxAPIConsumerTrialDays }

func (s *Service) GetTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.trialStore(w)
	if !ok {
		return
	}
	trial, err := store.GetAPIConsumerTrial(r.Context(), acct.ID, consumer.AppID, consumer.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the consumer has no trial")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the consumer trial"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.trialResponse(trial))
}

// PutTrial starts a consumer's one trial at the next minute, or moves the
// end of a running one. A trial never ends or starts in the past.
func (s *Service) PutTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.trialStore(w)
	if !ok {
		return
	}
	var req api.PutAPIConsumerTrialRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	trial, err := store.GetAPIConsumerTrial(r.Context(), acct.ID, consumer.AppID, consumer.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrInternal("could not load the consumer trial"))
		return
	}
	next, problem := s.nextTrial(trial, err == nil, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	next.AccountID, next.AppID, next.ConsumerID = acct.ID, consumer.AppID, consumer.ID
	saved, err := store.PutAPIConsumerTrial(r.Context(), next)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the consumer trial"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.trial_set", acct.ID, map[string]any{"app_id": consumer.AppID, "consumer_id": consumer.ID,
		"starts_at": saved.StartsAt, "ends_at": saved.EndsAt})
	httpkit.WriteJSON(w, http.StatusOK, s.trialResponse(saved))
}

// nextTrial applies a request to the consumer's trial, if any.
func (s *Service) nextTrial(trial state.APIConsumerTrial, exists bool, req api.PutAPIConsumerTrialRequest) (state.APIConsumerTrial, *api.Problem) {
	next := s.nextMinute()
	if exists && !trial.EndsAt.After(s.now()) {
		return trial, api.NewProblem(http.StatusConflict, api.CodeConflict, "Trial already used", "a consumer gets one trial, and this one has ended")
	}
	if !exists {
		trial = state.APIConsumerTrial{StartsAt: next}
	}
	switch {
	case (req.Days == 0) == (req.EndsAt == nil):
		return trial, validation("Invalid trial", "set days or ends_at")
	case req.Days != 0 && !ValidTrialDays(req.Days):
		return trial, validation("Invalid trial", fmt.Sprintf("days must be 1 to %d", state.MaxAPIConsumerTrialDays))
	case req.Days != 0:
		trial.EndsAt = trial.StartsAt.AddDate(0, 0, req.Days)
	default:
		trial.EndsAt = req.EndsAt.UTC()
	}
	if !trial.EndsAt.Equal(trial.EndsAt.Truncate(time.Minute)) || trial.EndsAt.Before(next) || !trial.EndsAt.After(trial.StartsAt) ||
		trial.EndsAt.Sub(trial.StartsAt) > state.MaxAPIConsumerTrialDays*24*time.Hour {
		return trial, validation("Invalid trial", fmt.Sprintf("ends_at must be a UTC minute from the next minute, at most %d days after the trial starts",
			state.MaxAPIConsumerTrialDays))
	}
	return trial, nil
}

// EndTrial ends a running trial at the next minute, or removes one that has
// not started.
func (s *Service) EndTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, consumer, ok := s.Host.ConsumerRoute(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.trialStore(w)
	if !ok {
		return
	}
	trial, err := store.GetAPIConsumerTrial(r.Context(), acct.ID, consumer.AppID, consumer.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "the consumer has no trial")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the consumer trial"))
		return
	}
	next := s.nextMinute()
	switch {
	case !trial.EndsAt.After(next):
		// Already over by the next minute.
	case trial.StartsAt.Before(next):
		trial.EndsAt = next
		if _, err := store.PutAPIConsumerTrial(r.Context(), trial); err != nil {
			api.WriteProblem(w, api.ErrInternal("could not end the consumer trial"))
			return
		}
	default:
		if err := store.DeleteAPIConsumerTrial(r.Context(), acct.ID, consumer.AppID, consumer.ID); err != nil {
			api.WriteProblem(w, api.ErrInternal("could not remove the consumer trial"))
			return
		}
	}
	s.Host.Audit(r.Context(), "api_consumer.trial_ended", acct.ID, map[string]any{"app_id": consumer.AppID, "consumer_id": consumer.ID})
	w.WriteHeader(http.StatusNoContent)
}
