package apidserver

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rehearsal"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func scenarioColdStartDigest(req api.RegisterScenarioTestRequest) string {
	_, d, _ := api.ScenarioColdStartEnrollment(req)
	return d
}

func (s *server) scenarioColdStartSupported(w http.ResponseWriter, req api.RegisterScenarioTestRequest) bool {
	if p := rehearsal.ValidateColdStart(req, s.rehearsalColdStartEnabled); p != nil {
		api.WriteProblem(w, p)
		return false
	}
	if req.ColdStart != nil {
		if _, ok := s.store.(rehearsalcold.Store); !ok {
			api.WriteProblem(w, api.ErrCapacity("cold-start receipt storage unavailable"))
			return false
		}
	}
	return true
}

// Durable preparation is honored even when admission is subsequently disabled.
func (s *server) beginScenarioColdPark(ctx context.Context, account, app string, fresh bool) (bool, error) {
	member, err := s.store.ScenarioTestMemberByApp(ctx, app)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if member.ColdStartJSON == "" {
		return false, nil
	}
	if !fresh {
		return false, state.ErrConflict
	}
	store, ok := s.store.(rehearsalcold.Store)
	if !ok {
		return false, state.ErrConflict
	}
	_, err = store.BeginScenarioColdPreparation(ctx, account, app)
	return err == nil, err
}

func (s *server) completeScenarioColdPark(ctx context.Context, account, app string) error {
	store, ok := s.store.(rehearsalcold.Store)
	if !ok {
		return state.ErrConflict
	}
	return store.CompleteScenarioColdPreparation(ctx, account, app)
}
