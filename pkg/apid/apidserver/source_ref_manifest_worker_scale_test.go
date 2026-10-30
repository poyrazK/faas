package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
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
