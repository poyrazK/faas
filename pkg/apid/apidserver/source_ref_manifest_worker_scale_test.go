package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A repository deploy applies worker.scale as the app's queue scaling policy,
// on an app that the same manifest makes a worker (ADR-20261018101500
// amendment 2026-10-27).
func TestSourceRefManifestAppliesWorkerScale(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	manifest := &gregalemanifest.Manifest{Worker: &gregalemanifest.WorkerSpec{
		DrainTimeout: "30s",
		Scale:        gregalemanifest.WorkerScaleSpec{Min: 0, Max: 3, Metric: api.ScalingMetricQueueDepth, Target: 100},
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true); problem != nil {
		t.Fatalf("apply manifest: %s: %s", problem.Code, problem.Detail)
	}
	updated, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Manifest.ExecutionMode != api.ExecutionModeWorker {
		t.Fatalf("execution mode = %q, want worker", updated.Manifest.ExecutionMode)
	}
	policy := updated.ScalingPolicy
	if policy == nil || policy.MinInstances != 0 || policy.MaxInstances != 3 || policy.ScaleOutCooldownS < 1 || policy.ScaleInCooldownS < 5 {
		t.Fatalf("scaling policy = %+v, want min 0, max 3 and default cooldowns", policy)
	}
	if target, ok := policy.TargetFor(api.ScalingMetricQueueDepth); !ok || target != 100 {
		t.Fatalf("queue_depth target = %v, %v; want 100", target, ok)
	}
}

// A repository deploy applies queue_bindings as `gregale deploy` does: pull
// worker bindings by name, a binding the manifest drops is disabled, and a
// deploy that is not accepted puts every binding back (ADR-20261018101500
// amendment 2026-10-31).
func TestSourceRefManifestAppliesQueueBindings(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	worker := &gregalemanifest.WorkerSpec{Scale: gregalemanifest.WorkerScaleSpec{Max: 3, Metric: api.ScalingMetricQueueDepth, Target: 100}}
	first := &gregalemanifest.Manifest{Worker: worker, QueueBindings: []gregalemanifest.QueueBinding{{Name: "jobs", QueueName: "jobs", MaxConcurrency: 4}}}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, first, "", true); problem != nil {
		t.Fatalf("apply manifest: %s: %s", problem.Code, problem.Detail)
	}
	byName := func() map[string]state.QueueBinding {
		t.Helper()
		rows, err := store.ListQueueBindingsForApp(ctx, acct.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]state.QueueBinding{}
		for _, row := range rows {
			out[row.Name] = row
		}
		return out
	}
	jobs := byName()["jobs"]
	if jobs.ID == "" || jobs.Mode != "pull" || jobs.WorkloadClass != state.WorkloadClassWorker || !jobs.Enabled || jobs.MaxConcurrency != 4 {
		t.Fatalf("jobs binding = %+v, want an enabled pull worker binding with concurrency 4", jobs)
	}
	second := &gregalemanifest.Manifest{Worker: worker, QueueBindings: []gregalemanifest.QueueBinding{{Name: "emails", QueueName: "emails"}}}
	staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, second, "", true)
	if problem != nil {
		t.Fatalf("apply second manifest: %s: %s", problem.Code, problem.Detail)
	}
	rows := byName()
	if rows["jobs"].Enabled || rows["emails"].ID == "" || !rows["emails"].Enabled {
		t.Fatalf("after second deploy jobs=%+v emails=%+v, want jobs disabled and emails enabled", rows["jobs"], rows["emails"])
	}
	if err := srv.RollbackSourceRefManifest(ctx, staged); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rows = byName()
	if _, ok := rows["emails"]; ok || !rows["jobs"].Enabled || rows["jobs"].ID != jobs.ID {
		t.Fatalf("after rollback = %+v, want jobs enabled again and emails gone", rows)
	}
}
