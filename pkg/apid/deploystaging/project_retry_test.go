package deploystaging

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type retryFixtureStore struct {
	app        state.App
	deployment state.Deployment
}

func (s retryFixtureStore) AppByID(context.Context, string) (state.App, error) { return s.app, nil }
func (s retryFixtureStore) LatestDeployment(context.Context, string) (state.Deployment, error) {
	return s.deployment, nil
}
func TestProjectRetryOwnershipSelectionAndScope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mutate      func(*retryFixtureStore)
		environment string
		valid       bool
	}{
		{"valid", func(*retryFixtureStore) {}, "", true},
		{"production alias", func(*retryFixtureStore) {}, "production", true},
		{"account", func(s *retryFixtureStore) { s.app.AccountID = "other" }, "", false},
		{"project", func(s *retryFixtureStore) { s.app.ProjectID = "other" }, "", false},
		{"deleted", func(s *retryFixtureStore) { s.app.Status = state.AppDeleted }, "", false},
		{"selector", func(s *retryFixtureStore) { s.app.WorkloadName = "other" }, "", false},
		{"newer", func(s *retryFixtureStore) { s.deployment.ID = "newer" }, "", false},
		{"healthy", func(s *retryFixtureStore) { s.deployment.Status = state.DeployLive }, "", false},
		{"scope", func(*retryFixtureStore) {}, "staging", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := retryFixtureStore{app: state.App{ID: "app", AccountID: "owner", ProjectID: "project", WorkloadName: "back", Status: state.AppActive}, deployment: state.Deployment{ID: "failed", Status: state.DeployFailed, Scope: state.DefaultEnvScope}}
			tc.mutate(&store)
			err := ValidateProjectRetry(t.Context(), store, "owner", "project", tc.environment, map[string]bool{"back": true}, map[string]string{"app": "failed"})
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
