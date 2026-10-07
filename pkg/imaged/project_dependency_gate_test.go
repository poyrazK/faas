// adr: 646
package imaged

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectDependencyActivationOutboxRecovery(t *testing.T) {
	for _, outcome := range []string{"ready", "failed", "timeout", "during_smoke"} {
		t.Run(outcome, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			ctx := t.Context()
			store := state.NewPgStore(pool)
			account, err := store.CreateAccount(ctx, "dependency-activation@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "dependency-activation"})
			if err != nil {
				t.Fatal(err)
			}
			createApp := func(name string) state.App {
				t.Helper()
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256})
				if err != nil {
					t.Fatal(err)
				}
				return app
			}
			backend, web := createApp("backend"), createApp("web")
			createDep := func(app state.App) state.Deployment {
				t.Helper()
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "docker.io/library/nginx:1.27"})
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			previous := createDep(web)
			if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
				t.Fatal(err)
			}
			manifest := web.Manifest
			manifest.ServiceBindings = []api.AppServiceBinding{{Service: "backend", Binding: api.ServiceBindingEnvKey("backend")}}
			manifest.ProjectDependencyConditions = map[string]string{"backend": api.ComposeDependencyHealthy}
			if _, err := store.UpdateApp(ctx, web.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			dependency, candidate := createDep(backend), createDep(web)
			if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			payload := `{"deployment_id":"` + candidate.ID + `","storage_key":"snap/` + candidate.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`
			var outboxID int64
			if err := pool.QueryRow(ctx, `INSERT INTO notification_outbox (channel,payload,available_at) VALUES ($1,$2,now()) RETURNING id`, db.NotifySnapshotWritten, payload).Scan(&outboxID); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			newHandler := func() *Handler {
				h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
				h.dependencyGateNow = func() time.Time { return now }
				return h
			}
			drain := func(handler *Handler) int {
				t.Helper()
				if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now() WHERE id=$1`, outboxID); err != nil {
					t.Fatal(err)
				}
				delivered, err := db.DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{db.NotifySnapshotWritten}, handler.HandleNotification, nil)
				if err != nil {
					t.Fatal(err)
				}
				return delivered
			}
			if delivered := drain(newHandler()); delivered != 0 {
				t.Fatalf("blocked notification acknowledged: %d", delivered)
			}
			current, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil || current.Status != state.DeploySnapshotting {
				t.Fatalf("candidate = %+v, %v", current, err)
			}
			live, err := store.LiveDeployment(ctx, web.ID)
			if err != nil || live.ID != previous.ID || live.TrafficPercent != 100 {
				t.Fatalf("old traffic changed: %+v, %v", live, err)
			}
			var attempts int
			if err := pool.QueryRow(ctx, `SELECT attempts FROM notification_outbox WHERE id=$1`, outboxID).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatalf("waiting consumed delivery attempts: %d, %v", attempts, err)
			}
			switch outcome {
			case "ready", "during_smoke":
				if err := store.MarkDeploymentLive(ctx, dependency.ID); err != nil {
					t.Fatal(err)
				}
			case "failed":
				if _, err := store.SetDeploymentFailed(ctx, dependency.ID, api.CodeImageManifestInvalid, "dependency failed"); err != nil {
					t.Fatal(err)
				}
			case "timeout":
				now = now.Add(api.ProjectDependencyGateTimeout)
			}
			restarted := newHandler()
			if outcome == "during_smoke" {
				restarted.WithHostingSmoke(func(ctx context.Context, _ state.App, _ state.Deployment) (apihostingreceipt.SmokeResult, error) {
					_, err := store.SetDeploymentFailed(ctx, dependency.ID, api.CodeImageManifestInvalid, "dependency failed during candidate smoke")
					return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/healthz", StatusCode: http.StatusOK}, err
				})
			}
			if delivered := drain(restarted); delivered != 1 {
				t.Fatalf("restart recovery delivered=%d", delivered)
			}
			current, err = store.DeploymentByID(ctx, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "ready" {
				if current.Status != state.DeployLive {
					t.Fatalf("ready candidate = %s: %s", current.Status, current.Error)
				}
			} else {
				wantCode := state.CodeDependencyFailed
				if outcome == "timeout" {
					wantCode = state.CodeDependencyTimeout
				}
				if current.Status != state.DeployFailed || current.ErrorCode != wantCode {
					t.Fatalf("blocked candidate = %s/%s, want failed/%s", current.Status, current.ErrorCode, wantCode)
				}
				var stages state.StageState
				if err := json.Unmarshal(current.StageState, &stages); err != nil || stages.DependencyGate == nil || stages.DependencyGate.Status != "failed" {
					t.Fatalf("terminal gate progress was not persisted: %s, %v", current.StageState, err)
				}
				live, err := store.LiveDeployment(ctx, web.ID)
				if err != nil || live.ID != previous.ID || live.TrafficPercent != 100 {
					t.Fatalf("failure changed old traffic: %+v, %v", live, err)
				}
			}
		})
	}
}
