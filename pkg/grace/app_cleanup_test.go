package grace_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRunAppsOnceDeletesArtifactsBeforeDatabaseState(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "app-grace@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "expired-artifacts", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: "expired-artifact-deployment", AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/expired-artifacts/rootfs.ext4"
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/unused/rootfs.ext4", key, 4); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, key, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	g := grace.New(grace.Params{
		Store: store, Artifacts: backend, Registry: registry, Now: time.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := g.RunAppsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Get(ctx, key); !storage.IsNotFound(err) {
		t.Fatalf("artifact Get error = %v, want storage.ErrNotFound", err)
	}
	if _, err := store.AppByID(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("app remains after artifact cleanup: %v", err)
	}
	if got := metricValue(t, registry, "apid_app_grace_deleted_total"); got != 1 {
		t.Fatalf("deleted_total = %v, want 1", got)
	}
	if got := metricValue(t, registry, "apid_app_grace_expired_artifact_bytes"); got != 0 {
		t.Fatalf("expired_artifact_bytes = %v, want 0", got)
	}
}

type legacyTombstoneStore struct {
	state.Store
	app       state.App
	artifacts []state.AppDeletionArtifact
	deleted   bool
}

func (s *legacyTombstoneStore) ListDeletedApps(context.Context) ([]state.App, error) {
	return []state.App{s.app}, nil
}

func (s *legacyTombstoneStore) ListAppDeletionArtifacts(context.Context, string) ([]state.AppDeletionArtifact, error) {
	return s.artifacts, nil
}

func (s *legacyTombstoneStore) DeleteAppPermanently(context.Context, string) error {
	s.deleted = true
	return nil
}

func TestRunAppsOnceQuarantinesMissingDeadline(t *testing.T) {
	store := &legacyTombstoneStore{
		Store:     state.NewMemStore(),
		app:       state.App{ID: "legacy", Slug: "legacy", Status: state.AppDeleted},
		artifacts: []state.AppDeletionArtifact{{Key: "apps/legacy/rootfs.ext4", Bytes: 4096}},
	}
	registry := prometheus.NewRegistry()
	g := grace.New(grace.Params{
		Store: store, Registry: registry, Now: time.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := g.RunAppsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.deleted {
		t.Fatal("legacy tombstone without a deadline was permanently deleted")
	}
	if got := metricValue(t, registry, "apid_app_grace_missing_deadlines"); got != 1 {
		t.Fatalf("missing_deadlines = %v, want 1", got)
	}
	if got := metricValue(t, registry, "apid_app_grace_expired_artifact_bytes"); got != 4096 {
		t.Fatalf("expired_artifact_bytes = %v, want 4096", got)
	}
}

func metricValue(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name || len(family.Metric) == 0 {
			continue
		}
		metric := family.Metric[0]
		switch family.GetType() {
		case clientmodel.MetricType_GAUGE:
			return metric.GetGauge().GetValue()
		case clientmodel.MetricType_COUNTER:
			return metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}
