package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type logDrainHealthTestStore struct {
	drains    map[string]state.AppLogDrain
	persisted []state.AppLogDrainHealth
}

func (s *logDrainHealthTestStore) ListEnabledAppLogDrains(context.Context) ([]state.AppLogDrain, error) {
	return nil, nil
}

func (s *logDrainHealthTestStore) AppLogDrainByID(_ context.Context, id string) (state.AppLogDrain, error) {
	drain, ok := s.drains[id]
	if !ok {
		return state.AppLogDrain{}, state.ErrNotFound
	}
	return drain, nil
}

func (s *logDrainHealthTestStore) AppLogDrainHealthByDrainID(_ context.Context, id string) (state.AppLogDrainHealth, error) {
	for i := len(s.persisted) - 1; i >= 0; i-- {
		if s.persisted[i].DrainID == id {
			return s.persisted[i], nil
		}
	}
	return state.AppLogDrainHealth{}, state.ErrNotFound
}

func (s *logDrainHealthTestStore) UpsertAppLogDrainHealth(_ context.Context, health state.AppLogDrainHealth) error {
	if _, ok := s.drains[health.DrainID]; !ok {
		return errors.New("foreign key violation")
	}
	s.persisted = append(s.persisted, health)
	return nil
}

func TestAppLogDrainManagerFlushHealthForgetsDeletedDrain(t *testing.T) {
	store := &logDrainHealthTestStore{drains: map[string]state.AppLogDrain{
		"drain-1": {ID: "drain-1"},
	}}
	manager := newAppLogDrainManager(store, nil, nil, nil, nil)
	manager.updateHealth("drain-1", func(health *state.AppLogDrainHealth) {
		health.Status = appLogDrainHealthHealthy
		health.DeliveredTotal = 4
	})
	manager.flushHealth(context.Background())
	if len(store.persisted) != 1 || store.persisted[0].DeliveredTotal != 4 {
		t.Fatalf("persisted health = %+v, want one snapshot with delivered_total=4", store.persisted)
	}

	delete(store.drains, "drain-1")
	manager.flushHealth(context.Background())
	manager.healthMu.Lock()
	_, stillTracked := manager.health["drain-1"]
	manager.healthMu.Unlock()
	if stillTracked {
		t.Fatal("deleted drain health remained in the manager after ErrNotFound")
	}
}

func TestAppLogDrainManagerRestoresPersistedHealth(t *testing.T) {
	store := &logDrainHealthTestStore{
		drains: map[string]state.AppLogDrain{"drain-1": {ID: "drain-1"}},
		persisted: []state.AppLogDrainHealth{{
			DrainID: "drain-1", Status: appLogDrainHealthDegraded, DeliveredTotal: 7,
			DroppedTotal: 2, LastError: "delivery queue dropped records",
		}},
	}
	manager := newAppLogDrainManager(store, nil, nil, nil, nil)
	manager.ensureHealth(context.Background(), state.AppLogDrain{ID: "drain-1"})

	manager.healthMu.Lock()
	got := manager.health["drain-1"]
	manager.healthMu.Unlock()
	if got.Status != appLogDrainHealthDegraded || !got.Active || got.DeliveredTotal != 7 || got.DroppedTotal != 2 || got.LastError == "" {
		t.Fatalf("restored health = %+v, want persisted counters and degraded status", got)
	}
}

func TestAppLogDrainErrorSummaryNeverReturnsRawError(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp 10.0.0.1:443: authorization=secret-value"),
		errors.New("log drain: endpoint returned status 500 body=secret-value"),
	} {
		if got := appLogDrainErrorSummary(err); got == err.Error() || got == "" {
			t.Fatalf("error summary exposed raw error: %q", got)
		}
	}
}
