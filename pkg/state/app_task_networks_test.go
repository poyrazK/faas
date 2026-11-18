// adr: 20261118070000 — app task VMs call the app's services as the app.
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type appTaskCallerLookup func(ctx context.Context, hostIP string) ([]state.Instance, error)

func TestAppTaskNetworkIdentityMem(t *testing.T) {
	store := state.NewMemStore()
	appTaskNetworkSuite(t, store, func(ctx context.Context, hostIP string) ([]state.Instance, error) {
		return store.AppTaskCallersByHostIP(ctx, "", hostIP)
	})
}

func TestAppTaskNetworkIdentityPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	appTaskNetworkSuite(t, store, func(ctx context.Context, hostIP string) ([]state.Instance, error) {
		return store.LiveServiceCallersByHostIP(ctx, state.DefaultLocalNodeName, hostIP)
	})
	// The fence holds against writes that bypass the store: a retired
	// identity never revives, and only a restoring lease owner inserts.
	for _, statement := range []string{
		`UPDATE app_task_networks SET active=true`,
		`UPDATE app_task_networks SET host_ip='10.200.0.7'`,
		`WITH gone AS (DELETE FROM app_task_networks RETURNING task_id,node_id,lease_token)
		 INSERT INTO app_task_networks(task_id,node_id,host_ip,lease_token) SELECT task_id,node_id,'10.200.0.8',lease_token FROM gone`,
	} {
		_, err := pool.Exec(ctx, statement)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("%s: err = %v, want check_violation", statement, err)
		}
	}
}

func appTaskNetworkSuite(t *testing.T, store state.Store, callers appTaskCallerLookup) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@task-network.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "task-net-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("1", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tasks := store.(state.AppTaskStore)
	networks := store.(state.AppTaskNetworkStore)
	now := time.Now().UTC().Truncate(time.Microsecond)
	params := state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual,
		Command: []string{"sh", "-c", "curl http://api.internal/"}, CreatedAt: now, MaxOutputBytes: 4096}
	queued, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	const hostIP = "10.200.0.6"
	if err := networks.RecordAppTaskNetwork(ctx, queued.ID, "", uuid.NewString(), hostIP); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("record for a queued task = %v, want ErrConflict", err)
	}
	if err := networks.RecordAppTaskNetwork(ctx, queued.ID, "", uuid.NewString(), "192.168.1.5"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("record outside 10/8 = %v, want ErrInvalidArgument", err)
	}
	task, err := tasks.ClaimNextAppTask(ctx, "task-network-test", now, time.Minute)
	if err != nil || task.ID != queued.ID || task.LeaseToken == nil {
		t.Fatalf("claim = %+v, %v", task, err)
	}
	if err := networks.RecordAppTaskNetwork(ctx, task.ID, "", uuid.NewString(), hostIP); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("record under another lease = %v, want ErrConflict", err)
	}
	if err := networks.RecordAppTaskNetwork(ctx, task.ID, "", *task.LeaseToken, hostIP); err != nil {
		t.Fatalf("record under the restoring lease: %v", err)
	}
	if err := networks.RecordAppTaskNetwork(ctx, task.ID, "", *task.LeaseToken, hostIP); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second record = %v, want ErrConflict", err)
	}
	assertAppTaskCallers(ctx, t, callers, hostIP, "", "") // restoring is not yet a caller
	if _, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertAppTaskCallers(ctx, t, callers, hostIP, app.ID, dep.ID)
	assertAppTaskCallers(ctx, t, callers, "10.200.0.10", "", "")
	if _, err = tasks.RequestAppTaskCancellation(ctx, account.ID, app.ID, task.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertAppTaskCallers(ctx, t, callers, hostIP, "", "") // a cancelled task stops being the app
	if err := networks.RetireAppTaskNetwork(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := networks.RetireAppTaskNetwork(ctx, task.ID); err != nil {
		t.Fatalf("retire is idempotent: %v", err)
	}

	// A second task on the same address: retiring it ends the identity
	// while it still runs.
	params.CreatedAt = now.Add(3 * time.Second)
	if _, err = tasks.CreateAppTask(ctx, params); err != nil {
		t.Fatal(err)
	}
	second, err := tasks.ClaimNextAppTask(ctx, "task-network-test", now.Add(3*time.Second), time.Minute)
	if err != nil || second.LeaseToken == nil {
		t.Fatalf("claim second = %+v, %v", second, err)
	}
	if err := networks.RecordAppTaskNetwork(ctx, second.ID, "", *second.LeaseToken, hostIP); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.MarkAppTaskRunning(ctx, second.ID, *second.LeaseToken, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertAppTaskCallers(ctx, t, callers, hostIP, app.ID, dep.ID)
	if err := networks.RetireAppTaskNetwork(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	assertAppTaskCallers(ctx, t, callers, hostIP, "", "")
}

func assertAppTaskCallers(ctx context.Context, t *testing.T, callers appTaskCallerLookup, hostIP, appID, deploymentID string) {
	t.Helper()
	got, err := callers(ctx, hostIP)
	if err != nil {
		t.Fatal(err)
	}
	if appID == "" {
		if len(got) != 0 {
			t.Fatalf("callers at %s = %+v, want none", hostIP, got)
		}
		return
	}
	if len(got) != 1 || got[0].AppID != appID || got[0].DeploymentID != deploymentID || got[0].HostIP != hostIP || got[0].State != string(state.StateRunning) {
		t.Fatalf("callers at %s = %+v, want app %s deployment %s", hostIP, got, appID, deploymentID)
	}
}
