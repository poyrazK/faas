// adr: 20261019200059 — HTTP service probes record no TLS stage.
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

func TestHTTPServiceVerificationOmitsTLSStage(t *testing.T) {
	spec := api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), Scope: "production", Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL",
		TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), ContextRevision: strings.Repeat("a", 64), Transport: api.ServiceBindingTransportHTTP}
	for _, scenario := range []string{"ordinary", "managed", "tls-claimed", "https-url", "managed-https-pin"} {
		t.Run(scenario, func(t *testing.T) {
			passed := api.ServiceBindingProbeCheck{Status: "passed"}
			report := api.ServiceBindingProbeReport{Service: spec.Service, Transport: api.ServiceBindingTransportHTTP, URL: "http://billing.svc.gregale:10081",
				DNS: passed, TLS: api.ServiceBindingProbeCheck{Status: "not_checked"}, Authorization: passed, Routing: passed}
			zero, now := 0, time.Now().UTC()
			task := state.BindingVerificationTask{TaskID: uuid.NewString(), DeploymentID: spec.CallerDeploymentID, Scope: spec.Scope, Status: string(state.AppTaskSucceeded), FinishedAt: &now, ExitCode: &zero,
				Pin: state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service}}
			pin := spec
			switch scenario {
			case "managed", "managed-https-pin":
				ctx := spec
				report.ManagedService, report.TargetDeploymentID, report.HTTPStatus = &ctx, spec.TargetDeploymentID, 204
				if scenario == "managed-https-pin" {
					pin.Transport = ""
					ctx.Transport = ""
				}
				task.Pin.TargetDeploymentID, task.Pin.Revision, task.Pin.ManagedService = spec.TargetDeploymentID, spec.ContextRevision, &pin
			case "tls-claimed":
				report.TLS = passed
			case "https-url":
				report.URL = "https://billing.internal"
			}
			raw, _ := json.Marshal(report)
			task.Stdout = string(raw)
			got := NormalizeVerification(task)
			want := scenario == "ordinary" || scenario == "managed"
			if (got.Result == "passed") != want {
				t.Fatalf("evidence=%+v", got)
			}
			if want && (len(got.Checks) != 3 || got.Checks[0].Name != "dns" || got.Checks[1].Name != "authorization" || got.Checks[2].Name != "routing") {
				t.Fatalf("checks=%+v", got.Checks)
			}
		})
	}
}
