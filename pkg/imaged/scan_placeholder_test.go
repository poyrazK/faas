package imaged

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

// H10-103: every verified staging attempt used to replace the shared scan
// sidecar with a 9999 refusal before deciding anything. A stage that failed
// before rebuilding (an unreachable registry, a deferred child base) left the
// refusal on unchanged bytes, and vmmd refused every cold boot on that base
// until a later stage rewrote it: 503 scan_critical on production-us
// 2026-10-16 10:02-11:14 UTC. Only an actual rebuild may invalidate it.
func TestVerifiedBaseFailureBeforeRebuildKeepsScanSidecar(t *testing.T) {
	h, _, puller, _, ref := verifiedBaseFixture(t, "complete")
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	be, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	const key, digestKey = "base/verified.ext4", "base/verified.digest"
	if _, err := h.EnsureBaseExt4(t.Context(), ref, key, digestKey, "", "", ""); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	before := readBaseScanCompatibility(t, be, key)
	if before.Findings[SeverityCritical] == 9999 || before.ScannedAt.IsZero() {
		t.Fatalf("fixture: first stage must leave a complete sidecar, got %+v", before)
	}

	// A new guest-init defeats reuse, as on a release upgrade, and the
	// registry is unreachable, so the stage fails before any rebuild.
	if err := os.WriteFile(h.guestInitPath, []byte("NEWER INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	puller.resolveErr = errors.New("registry unreachable")
	if _, err := h.EnsureBaseExt4(t.Context(), ref, key, digestKey, "", "", ""); err == nil {
		t.Fatal("stage with an unreachable registry succeeded")
	}
	if after := readBaseScanCompatibility(t, be, key); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed stage replaced the sidecar of unchanged bytes:\nbefore %+v\nafter  %+v", before, after)
	}
}
