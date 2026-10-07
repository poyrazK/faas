package state_test

// adr: 681

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func healthNotificationStores(t *testing.T, test func(*testing.T, state.Store, state.AppHealthHistoryStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { s := state.NewMemStore(); test(t, s, s) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		s := state.NewPgStore(pool)
		test(t, s, s)
	})
}

func healthNotificationFixture(t *testing.T, s state.Store) (state.Account, state.App) {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@health-notification.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "notification-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func healthNotificationAssessment(appID, status string, at time.Time) api.AppHealthResponse {
	return api.AppHealthResponse{AppID: appID, Scope: "default", Status: status, Phase: "serving", Summary: "Recorded serving assessment.", EvaluatedAt: at.Format(time.RFC3339Nano), ValidForSeconds: 120,
		ServingDeploymentIDs: []string{}, Capacity: api.AppHealthCapacity{Known: true, Required: 1, Ready: 1}, Checks: []api.AppHealthCheck{}}
}

func recordHealthNotificationAssessment(t *testing.T, s state.AppHealthHistoryStore, appID, status string, at time.Time) {
	t.Helper()
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), at)
	if err != nil || claim.AppID != appID {
		t.Fatalf("claim %+v: %v", claim, err)
	}
	a := healthNotificationAssessment(appID, status, at.Add(time.Millisecond))
	if err := s.FinishAppHealth(t.Context(), claim, a, at.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}
