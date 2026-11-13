package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAppOOMRecommendationNamesTheKillAndTheRAMCommand(t *testing.T) {
	got := apidserver.AppOOMRecommendation("retail-orders", api.DebugStatusOOMKill{DeploymentID: "live", PeakMB: 520, PlanMB: 512}, 3, "live")
	if got.Code != api.CodeAppRuntimeOOM || got.Severity != "error" {
		t.Fatalf("code/severity = %s/%s", got.Code, got.Severity)
	}
	for _, want := range []string{"3 out-of-memory kills in the last 24 hours", "520 MB on a 512 MB limit", "requests it was serving failed or timed out"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message missing %q: %s", want, got.Message)
		}
	}
	if !strings.Contains(got.Next, "`gregale app retail-orders --ram 1024`") {
		t.Errorf("next = %s", got.Next)
	}
}

func TestAppOOMRecommendationOnEarlierDeploymentIsAWarning(t *testing.T) {
	got := apidserver.AppOOMRecommendation("web", api.DebugStatusOOMKill{DeploymentID: "old", PlanMB: 256, DeploymentFailed: true}, 1, "live")
	if got.Severity != "warning" {
		t.Fatalf("severity = %s", got.Severity)
	}
	for _, want := range []string{"1 out-of-memory kill in", "hit the 256 MB limit", "its deployment failed", "earlier deployment"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message missing %q: %s", want, got.Message)
		}
	}
}

func TestAppOOMRecommendationWithoutSizesStillGivesTheCommand(t *testing.T) {
	got := apidserver.AppOOMRecommendation("web", api.DebugStatusOOMKill{}, 1, "")
	if got.Severity != "error" || !strings.Contains(got.Next, "--ram <MB>") {
		t.Fatalf("got %+v", got)
	}
}
