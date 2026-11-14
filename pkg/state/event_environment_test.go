package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stage's subscriptions receive only the stage's events, and production's
// only production's. An event's environment must be one of its account's.
//
// adr: 20261104120000
func TestEventFanoutKeepsEnvironment(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		label := "event-env-" + uuid.NewString()[:8]
		account, err := store.CreateAccount(ctx, label+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: label})
		if err != nil {
			t.Fatal(err)
		}
		staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: label, Type: state.AppTypeApp,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		production, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "*", nil)
		if err != nil {
			t.Fatal(err)
		}
		stage, inserted, err := store.UpsertEventSubscriptionInScope(ctx, account.ID, app.ID, "staging", "orders", "*", nil)
		if err != nil || !inserted {
			t.Fatalf("stage subscription: inserted %v, err %v", inserted, err)
		}
		if stage.ID == production.ID || stage.DeploymentScope != "staging" || stage.EnvironmentID != staging.ID ||
			production.DeploymentScope != "" || production.EnvironmentID != "" {
			t.Fatalf("subscriptions: production %+v, stage %+v", production, stage)
		}
		if _, _, err := store.UpsertEventSubscriptionInScope(ctx, account.ID, app.ID, "missing", "orders", "*", nil); err == nil {
			t.Fatal("a subscription was created in an environment the project lacks")
		}

		publish := func(environmentID string) ([]state.PublishedEventRecipient, error) {
			t.Helper()
			envelope, err := (events.Envelope{ID: uuid.NewString(), Source: "orders", Type: "order.created",
				Data: json.RawMessage(`{}`), EnvironmentID: environmentID}).Normalize(account.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
				return nil, err
			}
			work, err := store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			return work.RecipientSnapshot, nil
		}
		got, err := publish("")
		if err != nil || len(got) != 1 || got[0].ID != production.ID || got[0].DeploymentScope != "" {
			t.Fatalf("production event reached %+v (err %v), want only %s", got, err, production.ID)
		}
		got, err = publish(staging.ID)
		if err != nil || len(got) != 1 || got[0].ID != stage.ID || got[0].DeploymentScope != "staging" || got[0].EnvironmentID != staging.ID {
			t.Fatalf("stage event reached %+v (err %v), want only %s", got, err, stage.ID)
		}

		other, err := store.CreateAccount(ctx, label+"-other@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		otherProject, err := store.CreateProject(ctx, state.Project{AccountID: other.ID, Slug: label + "-other"})
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: other.ID, ProjectID: otherProject.ID, Slug: "staging"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := publish(foreign.ID); err == nil {
			t.Fatal("an event was published into another account's environment")
		}
	})
}

// Publish routes resolve the environment an event names: an environment of
// the account, a stage slug of the app's project, and production as "".
//
// adr: 20261104120000
func TestEventEnvironmentResolution(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		label := "event-env-resolve-" + uuid.NewString()[:8]
		account, err := store.CreateAccount(ctx, label+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: label})
		if err != nil {
			t.Fatal(err)
		}
		staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
		if err != nil {
			t.Fatal(err)
		}
		prod, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "production"})
		if errors.Is(err, state.ErrConflict) {
			envs, listErr := store.ListProjectEnvironments(ctx, account.ID, project.ID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			for _, env := range envs {
				if env.Slug == "production" {
					prod = env
				}
			}
		} else if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: label, Type: state.AppTypeApp,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		other, err := store.CreateAccount(ctx, label+"-other@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name    string
			resolve func() (string, error)
			want    string
			missing bool
		}{
			{"account stage", func() (string, error) { return store.AccountEventEnvironment(ctx, account.ID, staging.ID) }, staging.ID, false},
			{"account production", func() (string, error) { return store.AccountEventEnvironment(ctx, account.ID, prod.ID) }, "", false},
			{"other account", func() (string, error) { return store.AccountEventEnvironment(ctx, other.ID, staging.ID) }, "", true},
			{"not a uuid", func() (string, error) { return store.AccountEventEnvironment(ctx, account.ID, "staging") }, "", true},
			{"app stage", func() (string, error) { return store.AppEventEnvironment(ctx, account.ID, app.ID, "staging") }, staging.ID, false},
			{"app production", func() (string, error) { return store.AppEventEnvironment(ctx, account.ID, app.ID, "production") }, "", false},
			{"app missing", func() (string, error) { return store.AppEventEnvironment(ctx, account.ID, app.ID, "nope") }, "", true},
			{"app of other account", func() (string, error) { return store.AppEventEnvironment(ctx, other.ID, app.ID, "staging") }, "", true},
		} {
			got, err := c.resolve()
			if c.missing != errors.Is(err, state.ErrNotFound) || (!c.missing && err != nil) || got != c.want {
				t.Errorf("%s: got %q, err %v; want %q, missing %v", c.name, got, err, c.want, c.missing)
			}
		}
	})
}
