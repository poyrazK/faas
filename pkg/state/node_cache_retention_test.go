package state_test

// adr: 20261114120000

import (
	"context"
	"testing"

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
