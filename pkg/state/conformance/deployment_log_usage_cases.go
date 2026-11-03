// adr: 20261102182856
package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// builderd's per-build byte cap re-derives its count from DeploymentLogUsage
// after a restart: the bytes of every stored line (octets, not runes) and
// whether the system truncation marker is among them, per deployment.
func testDeploymentLogUsage(t *testing.T, fx *Fixture) {
	t.Helper()
	other, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:log-usage", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage, err := fx.Store.DeploymentLogUsage(fx.Ctx, fx.Deployment.ID); err != nil || usage != (state.DeploymentLogUsage{}) {
		t.Fatalf("empty log usage = %+v, %v", usage, err)
	}
	for _, line := range []string{"step 1\n", "ünïcode\n"} {
		if _, err := fx.Store.AppendDeploymentLog(fx.Ctx, fx.Deployment.ID, "build", line); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fx.Store.AppendDeploymentLog(fx.Ctx, other.ID, state.DeploymentLogStreamSystem, "[other build truncated]\n"); err != nil {
		t.Fatal(err)
	}
	want := int64(len("step 1\n") + len("ünïcode\n"))
	if usage, err := fx.Store.DeploymentLogUsage(fx.Ctx, fx.Deployment.ID); err != nil || usage.Bytes != want || usage.Truncated {
		t.Fatalf("usage = %+v, %v; want %d bytes, not truncated", usage, err, want)
	}
	marker := "[build log truncated]\n"
	if _, err := fx.Store.AppendDeploymentLog(fx.Ctx, fx.Deployment.ID, state.DeploymentLogStreamSystem, marker); err != nil {
		t.Fatal(err)
	}
	if usage, err := fx.Store.DeploymentLogUsage(fx.Ctx, fx.Deployment.ID); err != nil || !usage.Truncated || usage.Bytes != want+int64(len(marker)) {
		t.Fatalf("usage with marker = %+v, %v", usage, err)
	}
	if usage, err := fx.Store.DeploymentLogUsage(fx.Ctx, "not-a-uuid"); err != nil || usage != (state.DeploymentLogUsage{}) {
		t.Fatalf("malformed id usage = %+v, %v", usage, err)
	}
}
