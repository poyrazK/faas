package fcvm

import "testing"

// Cold boot records the drive's layer key before it makes the jail, and
// making the jail unmounts stale binds. The key must survive that, or every
// capture uploads the whole drive instead of a delta (ADR-20261115213230).
func TestDriveBaseKeySurvivesBindUnmount(t *testing.T) {
	v := &JailerVMM{}
	v.setDriveBaseKey("inst", "layers/dep.ext4")
	if err := v.unmountBindMounts("inst"); err != nil {
		t.Fatalf("unmountBindMounts: %v", err)
	}
	if got := v.driveBaseKey("inst"); got != "layers/dep.ext4" {
		t.Fatalf("driveBaseKey after unmount = %q, want layers/dep.ext4", got)
	}
	v.setDriveBaseKey("inst", "")
	if got := v.driveBaseKey("inst"); got != "" {
		t.Fatalf("driveBaseKey after clear = %q, want empty", got)
	}
}
