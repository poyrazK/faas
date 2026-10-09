// worker_pool_e2e_test.go — KVM-free worker pool scaling acceptance.
//
// Worker pools were promoted to preview on a manifest unit test alone; no
// test crossed apid -> Postgres -> schedd -> vmmd for a worker app. This one
// declares a queue-driven worker through the customer API and lets the real
// schedd targets tick size the pool against real queue rows.
//
// The backlog is seeded with a future due time so it counts as queue depth
// but the drain never delivers it: with a fake vmmd the guest would answer
// instantly and the backlog would vanish before the scaler observed it. That
// makes this a test of the scaling loop, not of delivery semantics, which
// need a real guest and live in the metal suite.
package e2e_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_WorkerPool_ScalesWithQueueDepthAndStabilizesScaleIn(t *testing.T) {
	// Pro allows three workers per account; Hobby's single worker would
	// leave nothing to scale between.
	f := newNormalPathFixtureWithPlan(t, "worker-pool", api.PlanPro)
	if f == nil {
		return
	}
	limits := api.MustLimitsFor(api.PlanPro)
	poolMax := limits.WorkerReplicasMax

	// 1. Worker mode, replica bounds and the queue-depth policy all go through
	//    the customer API before the deployment exists, so the deployment is
	//    resolved with them.
	workerMode := api.ExecutionModeWorker
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug, api.UpdateAppRequest{
		ExecutionMode:  &workerMode,
		WorkerReplicas: &api.WorkerScaling{Min: 0, Max: poolMax},
		ScalingPolicy: &api.ScalingPolicy{
			Targets:           []api.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 1}},
			ScaleOutCooldownS: api.MinScaleOutCooldownS,
			ScaleInCooldownS:  api.MinScaleInCooldownS,
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH worker app: status=%d body=%s", status, body)
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + repeatHex("7"),
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark deployment live: %v", err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	app, err := f.store.AppByID(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	if got := app.Manifest.ExecutionMode; got != api.ExecutionModeWorker {
		t.Fatalf("execution mode did not survive apid -> Postgres: %q", got)
	}
	if got := workerCount(t, f); got != 0 {
		t.Fatalf("min=0 pool started with %d workers before any backlog", got)
	}

	// 2. A backlog larger than the cap demands the whole pool.
	due := time.Now().Add(30 * time.Minute)
	var backlog []string
	for range poolMax + 2 {
		inv, err := f.store.EnqueueInvocation(f.ctx, state.Invocation{
			AccountID: app.AccountID, AppID: app.ID, DeploymentScope: state.DefaultInvocationDeploymentScope(app),
			Source: state.InvocationQueue, DueAt: due,
		})
		if err != nil {
			t.Fatalf("enqueue backlog: %v", err)
		}
		backlog = append(backlog, inv.ID)
	}
	peak := 0
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) && peak < poolMax {
		n := workerCount(t, f)
		if n > peak {
			peak = n
		}
		if n > poolMax {
			t.Fatalf("pool reached %d workers, above the plan cap %d", n, poolMax)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if peak != poolMax {
		t.Fatalf("pool did not scale to the cap: peak=%d want=%d.\n"+
			"Check that schedd's targets trigger reconciles worker apps with a queue_depth target.",
			peak, poolMax)
	}
	workers := workerIDs(t, f)

	// 3. The backlog disappears. The pool must not shrink on the next tick:
	//    scale-in waits for the stabilization window.
	for _, id := range backlog {
		if err := f.store.CancelInvocation(f.ctx, id); err != nil {
			t.Fatalf("cancel backlog %s: %v", id, err)
		}
	}
	drained := time.Now()
	window := time.Duration(api.WorkerScaleInStabilizationSeconds) * time.Second
	for time.Since(drained) < window/3 {
		if n := workerCount(t, f); n != poolMax {
			t.Fatalf("pool shrank to %d %s after the queue drained; scale-in must wait %s",
				n, time.Since(drained).Round(time.Second), window)
		}
		time.Sleep(time.Second)
	}

	// 4. After the window the pool converges to min=0, every worker through
	//    the graceful stop RPC rather than a bare destroy.
	deadline = drained.Add(window + 45*time.Second)
	for time.Now().Before(deadline) && workerCount(t, f) > 0 {
		time.Sleep(500 * time.Millisecond)
	}
	if n := workerCount(t, f); n != 0 {
		t.Fatalf("pool still has %d workers %s after the queue drained", n, time.Since(drained).Round(time.Second))
	}
	if elapsed := time.Since(drained); elapsed < window-5*time.Second {
		t.Fatalf("pool scaled in after %s, inside the %s stabilization window", elapsed.Round(time.Second), window)
	}
	stopped := map[string]bool{}
	for _, id := range f.vmmd.StopCalls() {
		stopped[id] = true
	}
	for _, id := range workers {
		if !stopped[id] {
			t.Fatalf("worker %s left without a graceful StopInstance; stops=%v", id, f.vmmd.StopCalls())
		}
	}
}

// workerCount counts the app's worker instances that hold a slot.
func workerCount(t *testing.T, f *normalPathFixture) int {
	t.Helper()
	return len(workerIDs(t, f))
}

func workerIDs(t *testing.T, f *normalPathFixture) []string {
	t.Helper()
	rows, err := f.h.Pool.Query(context.Background(),
		`select id::text from instances
		  where app_id = $1 and mode = 'worker'
		    and state in ('waking','cold_booting','running','warm')
		  order by id`, f.app.ID)
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan worker: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list workers: %v", err)
	}
	return ids
}

func repeatHex(digit string) string {
	out := ""
	for range 64 {
		out += digit
	}
	return out
}
