package previewset

import (
	"strings"
	"testing"
)

func TestEvaluateSetFailsOnSetupError(t *testing.T) {
	members := []Member{{AppID: "root", Slug: "pr-1-api", PreviewOfSlug: "api", WorkloadName: "api",
		AppStatus: "active", PreviewState: "open", DeploymentStatus: PhaseLive}}
	if got := EvaluateSet(1, "root", members, ""); got.Phase != PhaseLive {
		t.Fatalf("no setup error = %+v, want live", got)
	}
	got := EvaluateSet(1, "root", members, "the plan allows 1 open PR preview environment(s)")
	if got.Phase != PhaseFailed || !strings.Contains(got.Summary, "Preview PR #1 failed: the plan allows 1") ||
		got.RootSlug != "pr-1-api" || got.LiveCount != 1 {
		t.Fatalf("setup error = %+v, want failed with the reason and the member projection kept", got)
	}
}
