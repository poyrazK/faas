package gateway

// adr: 611

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type confirmingSnapshotStore struct {
	*confirmingWeightsStore
	snapshot      DeploymentWeightsSnapshot
	snapshotError error
	observed      []DeploymentWeightsSnapshot
}

func (s *confirmingSnapshotStore) LiveDeployments(context.Context, string) ([]DeploymentWeightsRow, error) {
	return nil, errors.New("snapshot source must not fall back to a separate weight read")
}

func (s *confirmingSnapshotStore) DeploymentWeightsSnapshot(context.Context, string) (DeploymentWeightsSnapshot, error) {
	return s.snapshot, s.snapshotError
}

func (s *confirmingSnapshotStore) DeploymentWeightsInstalled(context.Context, string, []DeploymentWeightsRow) error {
	return errors.New("snapshot observer must receive revision and plan")
}

func (s *confirmingSnapshotStore) DeploymentWeightsSnapshotInstalled(ctx context.Context, appID string, snapshot DeploymentWeightsSnapshot) error {
	if err := s.confirmingWeightsStore.DeploymentWeightsInstalled(ctx, appID, snapshot.Rows); err != nil {
		return err
	}
	s.observed = append(s.observed, snapshot)
	return nil
}

func TestInstalledWeightsCarriesSameSnapshotRevisionAfterCacheSwap(t *testing.T) {
	store := &confirmingSnapshotStore{confirmingWeightsStore: &confirmingWeightsStore{}}
	backend := NewPGBackend(nil, nil, nil).WithStore(store)
	store.backend = backend
	for _, id := range []string{"old", "new"} {
		store.snapshot = DeploymentWeightsSnapshot{
			Rows: []DeploymentWeightsRow{{ID: id, TrafficPercent: 100}}, RoutingRevision: id + "-revision",
			Drain: &RuntimeUpgradeDrainPlan{OperationID: id + "-operation", DeploymentID: id, ServingDeploymentID: "predecessor", CutoverAt: time.Now().UTC()},
		}
		if err := backend.RefreshDeploymentWeights(t.Context(), "app"); err != nil {
			t.Fatal(err)
		}
		if got := store.observed[len(store.observed)-1]; !reflect.DeepEqual(got, store.snapshot) {
			t.Fatal("receipt metadata diverged from installed snapshot", got, store.snapshot)
		}
	}
	store.snapshotError = errors.New("synthetic snapshot outage")
	store.snapshot.Rows = []DeploymentWeightsRow{{ID: "uninstalled", TrafficPercent: 100}}
	if err := backend.RefreshDeploymentWeights(t.Context(), "app"); !errors.Is(err, store.snapshotError) {
		t.Fatal("snapshot failure hidden", err)
	}
	backend.tgtMu.RLock()
	defer backend.tgtMu.RUnlock()
	if len(store.observed) != 2 || backend.appsPicker["app"].weights[0].DeploymentID != "new" {
		t.Fatal("failed snapshot changed installed weights or published a receipt")
	}
}
