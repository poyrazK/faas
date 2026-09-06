package state

import "testing"

func TestSnapshotCapturePairKeys(t *testing.T) {
	for _, tc := range []struct{ key, tier, want string }{
		{"snap/dep/mem", "init", "snap/dep/vmstate"},
		{"snap/dep/warm/mem", "warm", "snap/dep/warm/vmstate"},
		{"snap/dep/captures/one/mem", "init", "snap/dep/captures/one/vmstate"},
		{"snap/dep/warm/captures/two/mem", "warm", "snap/dep/warm/captures/two/vmstate"},
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
	}
	if IsSnapshotCaptureKey(SnapMemKey("dep")) {
		t.Fatal("legacy key treated as deletable capture")
	}
}
