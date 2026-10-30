// adr: 909 — admission refusals of a deployment prime explain the app's size.
package sched_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// A deploy refused by node budgets read only "capacity_unavailable" plus the
// scheduler's ledger text, which names nodes. The deployment now says how much
// the app needs and what to lower.
func TestCustomerCapacityProblemNamesTheAppSize(t *testing.T) {
	ram, vcpu, cpu := int64(2056), int64(4), int64(500)
	internal := `RAM headroom: node "fsn-2" resident 46000 + 2056 requested exceeds the 47600 MB ceiling`
	for _, tc := range []struct {
		name    string
		blocker api.WorkerAdmissionBlocker
		want    []string
	}{
		{"memory", api.WorkerAdmissionBlocker{Code: "node_ram", Resource: "ram_mb", Requested: &ram}, []string{"2056 MB of memory", "--ram"}},
		{"guest vcpu", api.WorkerAdmissionBlocker{Code: "node_vcpu", Resource: "vcpu", Requested: &vcpu}, []string{"4 guest vCPUs"}},
		{"host cpu", api.WorkerAdmissionBlocker{Code: "node_cpu", Resource: "cpu_millicores", Requested: &cpu}, []string{"500 millicores", "--cpu-millicores"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem := sched.CustomerCapacityProblem(internal, tc.blocker)
			if problem == nil || problem.Code != api.CodeCapacity {
				t.Fatalf("problem = %+v, want capacity_unavailable", problem)
			}
			text := problem.Detail + " " + problem.Hint
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("problem %q missing %q", text, want)
				}
			}
			if strings.Contains(text, "fsn-2") {
				t.Errorf("problem names a node: %q", text)
			}
		})
	}
	if problem := sched.CustomerCapacityProblem(internal, api.WorkerAdmissionBlocker{Code: "no_admitting_nodes"}); problem != nil {
		t.Fatalf("a refusal without a sized blocker = %+v, want nil", problem)
	}
}
