package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEventStorageUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.EventStorageUsageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event storage usage store"))
		return
	}
	usage, err := store.EventStorageUsage(r.Context(), acct.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "read event storage usage", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to read event storage usage"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, usage)
}

func eventStorageCapacityProblem(err error) *api.Problem {
	var capacity *state.EventStorageCapacityError
	if !errors.As(err, &capacity) {
		return nil
	}
	retry := int64(api.EventStorageRetryAfterSeconds)
	problem := api.NewProblem(http.StatusTooManyRequests, "event_storage_capacity_exhausted", "Event storage capacity exhausted",
		fmt.Sprintf("retained customer event %s would reach %d; limit %d. Retry the same source and id after retained receipts are pruned or the plan is upgraded; delivery completion alone does not release retained storage.", capacity.Resource, capacity.Observed, capacity.Limit)).
		WithLimit(capacity.Limit, capacity.Observed).WithDocs("https://gregale.dev/docs/event-driven#internal-event-subscriptions")
	problem.RetryAfterSeconds = &retry
	if capacity.Resource == "bytes" {
		problem.LimitBytes = &capacity.Limit
		problem.ObservedBytes = &capacity.Observed
	}
	return problem
}

func writeEventStorageCapacity(w http.ResponseWriter, err error) bool {
	problem := eventStorageCapacityProblem(err)
	if problem == nil {
		return false
	}
	w.Header().Set("Retry-After", strconv.FormatInt(*problem.RetryAfterSeconds, 10))
	api.WriteProblem(w, problem)
	return true
}
