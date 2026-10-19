package bindings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedServiceVerificationRequiresExactReport(t *testing.T) {
	spec := api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), Scope: "production", Service: "billing", Binding: "CUSTOM_BILLING_URL", TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), ContextRevision: strings.Repeat("a", 64)}
	for _, scenario := range []string{"exact", "ordinary", "other-release", "other-revision", "other-target", "truncated", "nonzero-exit", "wrong-scope"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := spec
			check := api.ServiceBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
			report := api.ServiceBindingProbeReport{ManagedService: &ctx, Service: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, HTTPStatus: 204, URL: "https://billing.internal", DNS: check, TLS: check, Authorization: check, Routing: check}
			zero, now := 0, time.Now().UTC()
			task := state.BindingVerificationTask{TaskID: uuid.NewString(), DeploymentID: spec.CallerDeploymentID, Scope: spec.Scope, Status: string(state.AppTaskSucceeded), FinishedAt: &now, ExitCode: &zero,
				Pin: state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
			switch scenario {
			case "ordinary":
				report.ManagedService = nil
			case "other-release":
				ctx.ReleaseID = uuid.NewString()
			case "other-revision":
				ctx.ContextRevision = strings.Repeat("b", 64)
			case "other-target":
				report.TargetDeploymentID = uuid.NewString()
			case "truncated":
				task.OutputTruncated = true
			case "nonzero-exit":
				exit := 1
				task.ExitCode = &exit
			case "wrong-scope":
				task.Scope = "staging"
			}
			raw, _ := json.Marshal(report)
			task.Stdout = string(raw)
			got := NormalizeVerification(task)
			if (got.Result == "passed") != (scenario == "exact") {
				t.Fatalf("evidence=%+v", got)
			}
			public, _ := json.Marshal(got)
			if strings.Contains(string(public), "PRIVATE_DETAIL") {
				t.Fatal("raw guest output leaked into evidence")
			}
		})
	}
}
