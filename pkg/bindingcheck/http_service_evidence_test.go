// adr: 20261019200059 — HTTP service edges are verified without a TLS stage.
package bindingcheck

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEvaluateHTTPServiceEvidenceRequiresHTTPTransport(t *testing.T) {
	plain := []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}
	for _, tc := range []struct {
		name      string
		transport string
		checks    []api.BindingVerificationCheck
		passed    bool
	}{
		{"HTTP caller, HTTP probe", "http", plain, true},
		{"HTTPS caller, HTTP probe", "https", plain, false},
		{"unknown transport, HTTP probe", "", plain, false},
		{"HTTP caller, failed stage", "http", []api.BindingVerificationCheck{plain[0], plain[1], {Name: "routing", Status: "failed"}}, false},
		{"HTTP caller, missing stage", "http", plain[:2], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			inventory.Bindings[0].Transport = tc.transport
			inventory.Bindings[0].Verification.Checks = tc.checks
			report, err := Evaluate(inventory, policy, now)
			if err != nil {
				t.Fatal(err)
			}
			blocked := slices.Contains(codes(report.Blockers), "verification_unknown")
			if report.Passed != tc.passed || blocked == tc.passed {
				t.Fatalf("passed=%v blockers=%v", report.Passed, codes(report.Blockers))
			}
		})
	}
	// An HTTP caller may still present an HTTPS probe over the companion alias.
	inventory, policy, now := fixture()
	inventory.Bindings[0].Transport = "http"
	if report, err := Evaluate(inventory, policy, now); err != nil || !report.Passed {
		t.Fatalf("HTTPS evidence for an HTTP caller: %+v %v", report, err)
	}
}
