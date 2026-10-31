// adr: 193, 20261031190030 — the durable guard's refusal names the resource
// that ran out.
package sched_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production 2026-10-30: the guard refused a wake for vCPU
// (used_vcpu=28 budget_vcpu=24) and the customer and operator both read "RAM
// headroom". Each resource now keeps its own message and blocker.
func TestDurableGuardRefusalNamesItsResource(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	for _, tc := range []struct {
		detail, code, prefix string
	}{
		{"used_mb=8888 admit_mb=2056 ceiling_mb=8593 cap_mb=8593", "node_ram", "RAM headroom"},
		{"resource=vcpu used_vcpu=28 quiet_vcpu=0 admit_vcpu=4 budget_vcpu=24", "node_vcpu", "vCPU headroom"},
		{"resource=cpu used_cpu_millicores=9000 quiet_cpu_millicores=0 admit_cpu_millicores=1000 budget_cpu_millicores=9000", "node_cpu", "CPU headroom"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			refused := fmt.Errorf("state: %w: node=n1 %s", state.ErrNodeCapacity, tc.detail)
			err := e.NodeCapacityProblem(refused)
			var problem *api.Problem
			if !errors.As(err, &problem) || problem.Code != api.CodeCapacity || !strings.HasPrefix(problem.Detail, tc.prefix) {
				t.Fatalf("problem = %+v, want CodeCapacity starting %q", problem, tc.prefix)
			}
			if blockers := sched.AdmissionBlockers(err); len(blockers) != 1 || blockers[0].Code != tc.code {
				t.Fatalf("blockers = %+v, want %s", blockers, tc.code)
			}
		})
	}
	other := errors.New("boom")
	if got := e.NodeCapacityProblem(other); !errors.Is(got, other) {
		t.Fatalf("a non-capacity error was rewritten: %v", got)
	}
}
