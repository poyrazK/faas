// adr: 147 — amendment 2026-11-28: a retry of a Free image deploy that the old
// Free default refused takes the full-rootfs path.
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Production, 2026-11-28: a Free signup's image deploy failed three times with
// the old gate's error. Without this, retrying those deployments after the
// amendment copied full_rootfs_allow_auto=false and failed again.
func TestRetryFullRootfsAllowAuto(t *testing.T) {
	const legacy = "build app layer: imaged: full-rootfs dispatch: oci: app diff_ids not a prefix of base diff_ids (full-rootfs required): plan free does not auto-dispatch to full-rootfs; pass FullRootfsOverride=&true to opt in"
	tests := []struct {
		name      string
		allowAuto bool
		err       string
		want      bool
	}{
		{name: "old free refusal", err: legacy, want: true},
		{name: "explicit opt-out after the amendment", err: "imaged: full-rootfs dispatch: oci: app diff_ids not a prefix of base diff_ids (full-rootfs required): this image is not built on a Gregale runtime base and self-contained deploys are off for this deployment"},
		{name: "paid opt-out under the old text", err: "oci: app diff_ids not a prefix of base diff_ids (full-rootfs required): plan hobby does not auto-dispatch to full-rootfs; pass FullRootfsOverride=&true to opt in"},
		{name: "other failure", err: "imaged: pull manifest: 404"},
		{name: "already on", allowAuto: true, err: "imaged: pull manifest: 404", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := state.Deployment{ID: "dep-1", Status: state.DeployFailed, Kind: state.DeploymentKindImage,
				ImageDigest: "ghcr.io/example/ws-node@sha256:abc", FullRootfsAllowAuto: tt.allowAuto, Error: tt.err}
			retry, err := state.RetryDeploymentInput(src, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if retry.FullRootfsAllowAuto != tt.want {
				t.Fatalf("retry full_rootfs_allow_auto = %v, want %v", retry.FullRootfsAllowAuto, tt.want)
			}
		})
	}
}
