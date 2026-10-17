package bindings

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNormalizeServiceTargetEvidenceRejectsMismatchedRecipient(t *testing.T) {
	target := "11111111-1111-4111-8111-111111111111"
	zero := 0
	task := state.BindingVerificationTask{Pin: state.BindingVerificationPin{Type: api.BindingTypeService, Binding: "billing", TargetDeploymentID: target}, Status: string(state.AppTaskSucceeded), ExitCode: &zero, Stdout: `{"service":"billing","target_deployment_id":"TARGET","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`}
	for _, actual := range []string{"", target, "22222222-2222-4222-8222-222222222222"} {
		probe := task
		probe.Stdout = strings.ReplaceAll(probe.Stdout, "TARGET", actual)
		result := NormalizeVerification(probe)
		if (result.Result == "passed") != (actual == target) {
			t.Fatalf("target=%s result=%+v", actual, result)
		}
	}
}
