package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeManifestScalingClient struct {
	app     api.AppResponse
	updates []api.UpdateAppRequest
}

func (f *fakeManifestScalingClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, nil
}

func (f *fakeManifestScalingClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	f.updates = append(f.updates, req)
	f.app.ScalingPolicy = req.ScalingPolicy
	return f.app, nil
}

func TestManifestSavedScalingPolicy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `scaling:
  min_instances: 1
  max_instances: 3
  target:
    metric: rps
    value: 10
  concurrency_overflow: drop
  max_queue_depth: 17
  max_queue_wait_ms: 1250
`)
	fake := &fakeManifestScalingClient{}
	if err := applyManifestSavedSettingsForTest(t, fake, "api", dir); err != nil {
		t.Fatalf("applyManifestScalingPolicy: %v", err)
	}
	if len(fake.updates) != 1 || fake.updates[0].ScalingPolicy == nil {
		t.Fatalf("updates = %+v, want one scaling policy PATCH", fake.updates)
	}
	policy := fake.updates[0].ScalingPolicy
	if policy.MinInstances != 1 || policy.MaxInstances != 3 || policy.ScaleOutCooldownS != 5 || policy.ScaleInCooldownS != 60 ||
		policy.ConcurrencyOverflow != api.ConcurrencyOverflowDrop || policy.MaxQueueDepth != 17 || policy.MaxQueueWaitMS != 1250 {
		t.Fatalf("policy = %+v, want defaults 5/60", policy)
	}
	if err := applyManifestSavedSettingsForTest(t, fake, "api", dir); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	if len(fake.updates) != 1 {
		t.Fatalf("idempotent apply made %d PATCH calls, want 1", len(fake.updates))
	}
}

func applyManifestSavedSettingsForTest(t *testing.T, client deploySettingsClient, slug, dir string) error {
	t.Helper()
	steps, err := prepareDeploySettings(context.Background(), client, slug, dir)
	if err != nil {
		return err
	}
	session := &savedDeploySession{path: filepath.Join(t.TempDir(), "settings.json"), record: deploySessionRecord{AppSlug: slug, Settings: steps}}
	return session.applySettings(context.Background(), client)
}
