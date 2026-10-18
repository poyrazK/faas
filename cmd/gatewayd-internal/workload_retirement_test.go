// adr: 1331
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type retirementWeightStore struct{ appID, scope string }

func (s *retirementWeightStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return nil, nil
}
func (s *retirementWeightStore) EnvironmentWorkloadAdmissionRetired(_ context.Context, appID, scope string) (bool, error) {
	s.appID = appID
	s.scope = scope
	return true, nil
}
func TestWorkloadRetirementUsesProductionStoreAdapter(t *testing.T) {
	store := &retirementWeightStore{}
	adapter := weightsStoreAdapter{store: store}
	blocked, err := adapter.EnvironmentWorkloadAdmissionRetired(t.Context(), "original-app", "staging")
	if err != nil || !blocked || store.appID != "original-app" || store.scope != "staging" {
		t.Fatalf("production adapter lost scoped fence: blocked=%t err=%v store=%+v", blocked, err, store)
	}
	unsupported := weightsStoreAdapter{store: routingDeploymentStore{}}
	if _, err := unsupported.EnvironmentWorkloadAdmissionRetired(t.Context(), "app", "staging"); err == nil {
		t.Fatal("missing admission adapter failed open")
	}
}

type retirementRouteInvalidator struct {
	*fakeInvalidator
	cleared string
}

func (f *retirementRouteInvalidator) InvalidateWorkloadAdmission(appID string) { f.cleared = appID }
func (f *retirementRouteInvalidator) RefreshLiveTargets(ctx context.Context, appID string) error {
	if f.cleared != appID {
		return errors.New("route refresh preceded admission invalidation")
	}
	return f.fakeInvalidator.RefreshLiveTargets(ctx, appID)
}
func TestWorkloadRetirementInvalidatesAdmissionBeforeRouteAcknowledgement(t *testing.T) {
	invalidator := &retirementRouteInvalidator{fakeInvalidator: &fakeInvalidator{}}
	_, applied := handleDeploymentRouteInvalidation(t.Context(), invalidator, `{"app_id":"app-7","deployment_id":"original-deployment","generation":44}`, testLogger())
	if !applied || invalidator.cleared != "app-7" {
		t.Fatal("gateway could acknowledge withdrawal with stale admission")
	}
}
