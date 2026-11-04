package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronEnvironmentBackends runs fn against both stores with a project app
// whose project has a staging environment.
func cronEnvironmentBackends(t *testing.T, fn func(t *testing.T, ctx context.Context, s state.Store, appID string)) {
	seed := func(t *testing.T, ctx context.Context, s state.Store) string {
		t.Helper()
		label := fmt.Sprintf("cron-env-%d", time.Now().UnixNano())
		account, err := s.CreateAccount(ctx, label+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: label})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: label, Type: state.AppTypeApp,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	t.Run("memory", func(t *testing.T) {
		ctx := context.Background()
		s := state.NewMemStore()
		fn(t, ctx, s, seed(t, ctx, s))
	})
	t.Run("postgres", func(t *testing.T) {
		s, _, ctx := cronPgStore(t)
		fn(t, ctx, s, seed(t, ctx, s))
	})
}

// ADR-20261104120000: a cron belongs to one environment of its app. A stage
// may hold the same command cron as production; a stage HTTP cron is not
// admitted yet, and suspension for want of a live deployment stays inside
// the environment that lost it.
func TestCronEnvironmentScope(t *testing.T) {
	cronEnvironmentBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID string) {
		limits := api.MustLimitsFor(api.PlanPro)
		create := func(scope string, command []string) (state.Cron, error) {
			return s.CreateCronIfUnderQuotaWithOptions(ctx, appID, "0 9 * * *", "/", true, limits,
				state.CronOptions{Command: command, DeploymentScope: scope})
		}
		report := []string{"bin/report"}
		production, err := create("", report)
		if err != nil {
			t.Fatal(err)
		}
		staging, err := create("staging", report)
		if err != nil {
			t.Fatalf("staging copy of a production cron: %v", err)
		}
		if production.DeploymentScope != "" || staging.DeploymentScope != "staging" || staging.ID == production.ID {
			t.Fatalf("scopes = %q/%q (ids %s/%s)", production.DeploymentScope, staging.DeploymentScope, production.ID, staging.ID)
		}
		if named, err := create("production", report); err != nil || named.ID != production.ID {
			t.Fatalf("production by name = %+v, %v; want the production cron", named, err)
		}
		if _, err := create("staging", nil); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("staging HTTP cron: err = %v, want ErrInvalidArgument", err)
		}

		scoped, ok := s.(state.CronScopeSuspensionStore)
		if !ok {
			t.Fatalf("%T does not suspend crons per environment", s)
		}
		count, err := scoped.SuspendCronsForAppScope(ctx, appID, "staging", state.CronSuspendedNoLiveDeployment)
		if err != nil || count != 1 {
			t.Fatalf("suspend staging = %d, %v; want 1", count, err)
		}
		for _, want := range []struct {
			cron      state.Cron
			suspended bool
		}{{production, false}, {staging, true}} {
			got, err := s.CronByID(ctx, want.cron.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (got.SuspendedReason != "") != want.suspended || got.DeploymentScope != want.cron.DeploymentScope {
				t.Fatalf("cron %q suspended_reason = %q, want suspended %v", got.DeploymentScope, got.SuspendedReason, want.suspended)
			}
		}
		if !state.CronInScope(production, "default") || !state.CronInScope(production, "production") ||
			state.CronInScope(production, "staging") || !state.CronInScope(staging, "staging") || state.CronInScope(staging, "default") {
			t.Fatal("CronInScope does not keep crons in their environment")
		}
	})
}

// The database resolves a stage cron's environment from the app's project
// and refuses a slug the project does not have.
func TestPgCronEnvironmentMembership(t *testing.T) {
	s, _, ctx := cronPgStore(t)
	appID := seedCronApp(t, ctx, s, "env-membership")
	_, err := s.CreateCronIfUnderQuotaWithOptions(ctx, appID, "0 9 * * *", "/", true, api.MustLimitsFor(api.PlanPro),
		state.CronOptions{Command: []string{"bin/report"}, DeploymentScope: "staging"})
	if err == nil {
		t.Fatal("stage cron on an app without that environment was admitted")
	}
}
