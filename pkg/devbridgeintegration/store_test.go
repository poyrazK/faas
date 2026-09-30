package devbridgeintegration

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevBridgeStoreParity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			var store state.Store
			var fresh func() state.DevBridgeStore
			if kind == "memory" {
				store = state.NewMemStore()
				fresh = func() state.DevBridgeStore { return store.(state.DevBridgeStore) }
			} else {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				pg := state.NewPgStore(pool)
				store = pg
				fresh = func() state.DevBridgeStore { return state.NewPgStore(pool) }
			}
			bridges := store.(state.DevBridgeStore)
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "bridge-parity@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "development"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, WorkloadClass: state.WorkloadClassHTTP})
			if err != nil {
				t.Fatal(err)
			}
			scope := devbridge.Scope{AccountID: account.ID, DeveloperID: "alice", ProjectID: project.ID, EnvironmentID: env.ID, TargetAppID: app.ID}
			now := time.Now().UTC()
			session, credentials, err := devbridge.NewSession(scope, now, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, session); err != nil {
				t.Fatal(err)
			}
			// A fresh PgStore wrapper must recover credentials from persisted digests.
			stored, err := fresh().DevBridgeByID(ctx, account.ID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := stored.AuthorizeAttachment(now, credentials.AttachmentToken); err != nil {
				t.Fatal(err)
			}
			second, _, err := devbridge.NewSession(scope, now, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, second); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("session quota bypassed: %v", err)
			}
			if err := bridges.RevokeDevBridge(ctx, account.ID, session.ID, now); err != nil {
				t.Fatal(err)
			}
			if err := bridges.CreateDevBridge(ctx, second); err != nil {
				t.Fatalf("revocation did not release quota: %v", err)
			}
			stored, err = bridges.DevBridgeByID(ctx, account.ID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.AuthorizeAttachment(now, credentials.AttachmentToken) == nil {
				t.Fatal("stored revocation lost")
			}
			if _, err := bridges.DevBridgeByID(ctx, project.ID, session.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("cross-account read: %v", err)
			}
		})
	}
}
