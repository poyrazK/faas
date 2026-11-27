package fcvm

import (
	"context"
	"testing"
	"time"
)

// Cold boot records the drive's layer key before it makes the jail, and
// making the jail unmounts stale binds. The key must survive that, or every
// capture uploads the whole drive instead of a delta (ADR-20261115213230).
// Kill, the teardown, clears it.
func TestDriveBaseKeySurvivesJailUntilKill(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.setDriveBaseKey("inst", "layers/dep.ext4")
	if err := v.unmountBindMounts("inst"); err != nil {
		t.Fatalf("unmountBindMounts: %v", err)
	}
	if got := v.driveBaseKey("inst"); got != "layers/dep.ext4" {
		t.Fatalf("driveBaseKey after unmount = %q, want layers/dep.ext4", got)
	}
	if err := v.Kill(context.Background(), Lease{Instance: "inst", UID: 20000, GID: 20000}); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := v.driveBaseKey("inst"); got != "" {
		t.Fatalf("driveBaseKey after Kill = %q, want empty", got)
	}
}
