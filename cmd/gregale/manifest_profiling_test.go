package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type profileManifestClient struct {
	app     api.AppResponse
	updates int
}

func (c *profileManifestClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return c.app, nil
}
func (c *profileManifestClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	c.updates++
	c.app.Manifest.Profiling = req.Profiling
	return c.app, nil
}

func TestManifestProfilingStagesBeforeBuildAndCompensatesFailure(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "profiling:\n  enabled: true\n  window_seconds: 10\n")
	client := &profileManifestClient{}
	txn, err := stageManifestProfiling(t.Context(), client, "app", dir)
	if err != nil {
		t.Fatal(err)
	}
	if client.app.Manifest.Profiling == nil || !client.app.Manifest.Profiling.Enabled {
		t.Fatal("profiling not applied before build")
	}
	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.app.Manifest.Profiling.Enabled {
		t.Fatal("failed build left profiling enabled")
	}
	txn, err = stageManifestProfiling(t.Context(), client, "app", dir)
	if err != nil {
		t.Fatal(err)
	}
	txn.committed = true
	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !client.app.Manifest.Profiling.Enabled {
		t.Fatal("accepted deployment lost policy")
	}
	previous := client.updates
	if _, err := stageManifestProfiling(t.Context(), client, "app", dir); err != nil {
		t.Fatal(err)
	}
	if client.updates != previous {
		t.Fatal("idempotent deploy patched config")
	}
}

func TestManifestProfilingRollbackPreservesConcurrentEdit(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "profiling:\n  enabled: true\n")
	client := &profileManifestClient{}
	txn, err := stageManifestProfiling(t.Context(), client, "app", dir)
	if err != nil {
		t.Fatal(err)
	}
	client.app.Manifest.Profiling = &api.ProfilingConfig{Enabled: true, WindowSeconds: 30}
	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.app.Manifest.Profiling.WindowSeconds != 30 {
		t.Fatal("rollback overwrote concurrent edit")
	}
}
