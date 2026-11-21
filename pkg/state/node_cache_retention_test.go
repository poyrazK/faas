package state_test

// adr: 20261114182830

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSnapshotCapturePrefix: every object of an immutable capture maps to the
// capture's prefix; legacy mutable keys and other prefixes map to nothing, so
// the node cache sweep never touches them.
func TestSnapshotCapturePrefix(t *testing.T) {
	const capture = "snap/dep/captures/0b6e6c7a-9a51-4d3c-8f6e-2b1f6f7c9d10/v2/"
	for key, want := range map[string]string{
		capture + "mem":                            capture,
		capture + "vmstate":                        capture,
		capture + "drive":                          capture,
		capture + "backing":                        capture,
		"snap/dep/warm/captures/c/v2/mem":          "snap/dep/warm/captures/c/v2/",
		"snap/dep/mem":                             "",
		"snap/dep/vmstate":                         "",
		"apps/a/layer.ext4":                        "",
		"base/releases/runner-node22-amd64-x.ext4": "",
	} {
		if got := state.SnapshotCapturePrefix(key); got != want {
			t.Errorf("SnapshotCapturePrefix(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestMemStoreNodeCacheRetention: a non-stale snapshot's capture is live, a
// stale one's is stale, and an unknown capture is absent.
func TestMemStoreNodeCacheRetention(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	const (
		livePrefix  = "snap/dep-a/captures/c-live/v2/"
		stalePrefix = "snap/dep-b/captures/c-stale/v2/"
	)
	for _, s := range []state.Snapshot{
		{DeploymentID: "dep-a", StorageKey: livePrefix + "mem"},
		{DeploymentID: "dep-b", StorageKey: stalePrefix + "mem", Stale: true},
	} {
		if _, err := m.CreateSnapshot(ctx, s); err != nil {
			t.Fatalf("create snapshot %s: %v", s.StorageKey, err)
		}
	}
	got, err := m.NodeCacheRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]state.CaptureUse{livePrefix: state.CaptureLive, stalePrefix: state.CaptureStale}
	if len(got.Captures) != len(want) {
		t.Fatalf("captures = %v, want %v", got.Captures, want)
	}
	for prefix, use := range want {
		if got.Captures[prefix] != use {
			t.Errorf("captures[%s] = %v, want %v", prefix, got.Captures[prefix], use)
		}
	}
}

// TestMemStoreNodeCacheRetentionLiveLayers: a deployment with a non-stale
// snapshot keeps its layer (its rootfs key, or layers/<id>.ext4 when it has
// none) in LiveLayers; a deployment whose only snapshot is stale does not.
func TestMemStoreNodeCacheRetentionLiveLayers(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	a, err := m.CreateAccount(ctx, "layers@retention.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "layers", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(rootfsKey string, stale bool) state.Deployment {
		d, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeployLive, RootfsKey: rootfsKey})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, StorageKey: "snap/" + d.ID + "/captures/c/v2/mem", Stale: stale}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	keyed := mk("apps/layers/keyed.ext4", false)
	unkeyed := mk("", false)
	stale := mk("apps/layers/stale.ext4", true)
	got, err := m.NodeCacheRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{
		keyed.RootfsKey:                  true,
		"layers/" + unkeyed.ID + ".ext4": true,
		stale.RootfsKey:                  false,
	} {
		if got.LiveLayers[key] != want {
			t.Errorf("LiveLayers[%s] = %v, want %v", key, got.LiveLayers[key], want)
		}
	}
}
