package state_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorObservabilityStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			account, err := store.CreateAccount(ctx, "obs-store@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "obs-store-app", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			deployed, err := store.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, ImageDigest: "sha256:obs-test", AppID: app.ID, Status: state.DeployPending})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.UpdateDeploymentStatus(ctx, deployed.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			apps, live, err := store.OperatorTenantCounts(ctx, account.ID)
			if err != nil || apps != 1 || live != 1 {
				t.Fatalf("counts=(%d,%d) err=%v", apps, live, err)
			}
			now := time.Now().UTC()
			trace := "11111111111111111111111111111111"
			for i := 0; i < 2; i++ {
				entry := state.AuditLog{ID: uuid.New(), Kind: "operator.action.force_park", ReceivedAt: now}
				if i == 1 {
					entry.TraceID = &trace
				}
				if err = store.InsertAuditLog(ctx, entry); err != nil {
					t.Fatal(err)
				}
			}
			coverage, err := store.OperatorAuditTraceCoverage(ctx, now.Add(-time.Minute))
			if err != nil || coverage != 0.5 {
				t.Fatalf("coverage=%v err=%v", coverage, err)
			}
			if err = store.AppendEvent(ctx, "apid", "deployment.failed", &account.ID, []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			activity, err := store.OperatorOverviewActivity(ctx, now.Add(-time.Hour), now.Add(-time.Hour))
			if err != nil || activity.AuditEvents24h != 2 || len(activity.Failures) != 1 || activity.Failures[0].Count != 1 {
				t.Fatalf("activity=%+v err=%v", activity, err)
			}

			// A failure older than 200 newer active deployments must still be
			// reachable through both pagination and a pushed-down severity filter.
			base := now.Add(-time.Hour)
			for i := 0; i < 201; i++ {
				owned, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: fmt.Sprintf("obs-incident-%d", i), Type: state.AppTypeApp})
				if err != nil {
					t.Fatal(err)
				}
				status := state.DeployPending
				if i == 0 {
					status = state.DeployFailed
				}
				deployment, err := store.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, ImageDigest: "sha256:obs-test", AppID: owned.ID, Status: state.DeployPending, CreatedAt: base.Add(time.Duration(i) * time.Second)})
				if err != nil {
					t.Fatal(err)
				}
				if status == state.DeployFailed {
					if err = store.UpdateDeploymentStatus(ctx, deployment.ID, status, "test failure"); err != nil {
						t.Fatal(err)
					}
				}
			}
			filter := state.OperatorIncidentFilter{Kind: "deployment", Since: now.Add(-24 * time.Hour), StuckBefore: now.Add(-5 * time.Minute), StaleBefore: now.Add(-time.Minute), Limit: 200}
			rows, err := store.ListOperatorIncidents(ctx, filter)
			if err != nil || len(rows) != 200 {
				t.Fatalf("first page len=%d err=%v", len(rows), err)
			}
			filter.BeforeAt, filter.BeforeID = rows[199].ObservedAt, rows[199].ID
			rows, err = store.ListOperatorIncidents(ctx, filter)
			if err != nil || len(rows) != 1 || rows[0].Severity != "error" {
				t.Fatalf("tail=%+v err=%v", rows, err)
			}
			filter.BeforeAt, filter.BeforeID, filter.Severity = time.Time{}, "", "error"
			rows, err = store.ListOperatorIncidents(ctx, filter)
			if err != nil || len(rows) != 1 {
				t.Fatalf("severity page=%+v err=%v", rows, err)
			}
		})
	}
}
