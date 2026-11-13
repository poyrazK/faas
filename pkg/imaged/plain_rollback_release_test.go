package imaged

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlainRollbackReportsMigrationStartedDuringSmoke(t *testing.T) {
	store, ctx := state.NewMemStore(), t.Context()
	acct, err := store.CreateAccount(ctx, "rollback-release@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rollback-release", RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	create := func(command []string, live bool) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), ReleaseCommand: command, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err = store.UpdateDeploymentStatus(ctx, d.ID, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if live {
			if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	target, current := create(nil, true), create(nil, true)
	if _, err = store.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmoke(func(ctx context.Context, _ state.App, _ state.Deployment) (apihostingreceipt.SmokeResult, error) {
		next, now := create([]string{"bin/migrate"}, false), time.Now().UTC()
		task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: next.ID, Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimNextAppTask(ctx, "late-release", now, time.Minute)
		if err != nil || claimed.ID != task.ID {
			t.Fatalf("claim release: %+v %v", claimed, err)
		}
		if _, err = store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: http.StatusOK, Path: "/healthz"}, nil
	})
	h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + target.ID + `","vmstate_path":"/tmp/rollback/vmstate","storage_key":"snap/` + target.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`})
	got, err := store.DeploymentByID(ctx, target.ID)
	if err != nil || got.Status != state.DeploySuperseded || got.TrafficPercent != 0 || got.ErrorCode != api.CodeRollbackCrossesRelease {
		t.Fatalf("rollback did not retain migration refusal: %+v %v", got, err)
	}
	serving, err := store.DeploymentByID(ctx, current.ID)
	if err != nil || serving.TrafficPercent != 100 {
		t.Fatalf("migration refusal replaced serving code: %+v %v", serving, err)
	}
}
