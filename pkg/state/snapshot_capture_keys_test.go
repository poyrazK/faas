package state

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSnapshotCapturePairKeys(t *testing.T) {
	for _, tc := range []struct{ key, tier, want string }{
		{"snap/dep/mem", "init", "snap/dep/vmstate"},
		{"snap/dep/warm/mem", "warm", "snap/dep/warm/vmstate"},
		{"snap/dep/captures/one/mem", "init", "snap/dep/captures/one/vmstate"},
		{"snap/dep/warm/captures/two/mem", "warm", "snap/dep/warm/captures/two/vmstate"},
		{"snap/dep/captures/one/v2/mem", "init", "snap/dep/captures/one/v2/vmstate"},
		{"snap/dep/warm/captures/two/v2/mem", "warm", "snap/dep/warm/captures/two/v2/vmstate"},
	} {
		if got := SnapshotVMStateKey(Snapshot{DeploymentID: "dep", Tier: tc.tier, StorageKey: tc.key}); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.key, got, tc.want)
		}
	}
	for _, tier := range []string{SnapshotTierInit, SnapshotTierWarm} {
		a, b := SnapshotCaptureMemKey("dep", tier, "one"), SnapshotCaptureMemKey("dep", tier, "two")
		if a == b || !IsSnapshotCaptureKey(a) || !IsSnapshotCaptureKey(b) {
			t.Fatalf("capture namespace %q %q", a, b)
		}
		if drive := SnapshotDriveKey(Snapshot{StorageKey: a}); drive != strings.TrimSuffix(a, "/mem")+"/drive" {
			t.Fatalf("drive key for %q = %q", a, drive)
		}
	}
	if drive := SnapshotDriveKey(Snapshot{StorageKey: "snap/dep/captures/legacy/mem"}); drive != "" {
		t.Fatalf("legacy capture unexpectedly has coupled drive %q", drive)
	}
	if IsSnapshotCaptureKey(SnapMemKey("dep")) {
		t.Fatal("legacy key treated as deletable capture")
	}
}

func TestSnapshotCaptureTokenRequiresCanonicalCoupledNamespace(t *testing.T) {
	token := uuid.NewString()
	for _, tier := range []string{SnapshotTierInit, SnapshotTierWarm} {
		if got, ok := SnapshotCaptureToken(SnapshotCaptureMemKey("dep", tier, token)); !ok || got != token {
			t.Fatal("valid capture token was lost")
		}
	}
	for _, key := range []string{SnapMemKey("dep"), "snap/dep/captures/" + token + "/mem", SnapshotCaptureMemKey("dep", SnapshotTierInit, "arbitrary"), SnapshotCaptureMemKey("dep", SnapshotTierInit, uuid.Nil.String()), SnapshotCaptureMemKey("dep", SnapshotTierInit, strings.ReplaceAll(token, "-", ""))} {
		if _, ok := SnapshotCaptureToken(key); ok {
			t.Fatal("noncanonical namespace gained a catalog token")
		}
	}
}
