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

// A route first served by a stage deployment lands in the stage's own
// inventory and fires a first-seen event carrying the stage; production
// still sees its own first hit of the same route, and its inventory never
// lists the stage's routes.
//
// adr: 20261104120000
func TestAPIDiscoveryKeepsEnvironment(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		label := "route-env-" + uuid.NewString()[:8]
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
		production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:prod", Scope: "default"})
		if err != nil {
			t.Fatal(err)
		}
		stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stage", Scope: "staging"})
		if err != nil {
			t.Fatal(err)
		}

		now := time.Now().UTC().Truncate(time.Second)
		serve := func(deploymentID string) *events.Envelope {
			t.Helper()
			event := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
				ConsumerKey: state.AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute), RequestCount: 1, BillableUnits: 1,
				DiscoveredRoute: "GET /orders/{id}", DiscoveredAt: now, DiscoveredDeploymentID: deploymentID}
			if _, err := store.(state.ConsumerUsageStore).RecordAPIConsumerUsage(ctx, event); err != nil {
				t.Fatal(err)
			}
			work, err := store.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour))
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			if err != nil {
				t.Fatal(err)
			}
			var envelope events.Envelope
			if err := json.Unmarshal(work.Payload, &envelope); err != nil {
				t.Fatal(err)
			}
			if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
			return &envelope
		}

		first := serve(stage.ID)
		if first == nil || first.EnvironmentID != staging.ID {
			t.Fatalf("stage first sighting event = %+v, want environment %s", first, staging.ID)
		}
		if again := serve(stage.ID); again != nil {
			t.Fatalf("a stage's second sighting fired %+v", again)
		}
		routes, _, err := store.(state.APIRouteInventoryStore).ListDiscoveredAPIRoutes(ctx, account.ID, app.ID, 500)
		if err != nil || len(routes) != 0 {
			t.Fatalf("production inventory lists stage routes: %+v (err %v)", routes, err)
		}
		prod := serve(production.ID)
		if prod == nil || prod.EnvironmentID != "" || prod.ID == first.ID {
			t.Fatalf("production first sighting event = %+v, want unstamped and distinct from %s", prod, first.ID)
		}
		routes, _, err = store.(state.APIRouteInventoryStore).ListDiscoveredAPIRoutes(ctx, account.ID, app.ID, 500)
		if err != nil || len(routes) != 1 || routes[0].RequestCount != 1 {
			t.Fatalf("production inventory = %+v (err %v), want one route seen once", routes, err)
		}
	})
}
