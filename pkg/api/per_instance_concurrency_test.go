// adr: 20261022203523
package api

import (
	"net/http"
	"testing"
	"time"
)

func TestLimitsPerInstanceConcurrency(t *testing.T) {
	for _, tc := range []struct {
		plan      Plan
		requested int
		want      int
	}{
		{plan: PlanFree, requested: 0, want: 4},
		{plan: PlanFree, requested: 1, want: 1},
		{plan: PlanFree, requested: 4, want: 4},
		{plan: PlanFree, requested: 5, want: 4},
		{plan: PlanHobby, requested: 2, want: 2},
		{plan: PlanPro, requested: -1, want: 25},
		{plan: PlanScale, requested: 2, want: 2},
		{plan: PlanScale, requested: 0, want: 80},
	} {
		if got := MustLimitsFor(tc.plan).PerInstanceConcurrency(tc.requested); got != tc.want {
			t.Errorf("%s PerInstanceConcurrency(%d) = %d, want %d", tc.plan, tc.requested, got, tc.want)
		}
	}
}

func TestValidatePerInstanceConcurrency(t *testing.T) {
	for _, tc := range []struct {
		plan      Plan
		requested int
		ok        bool
	}{
		{plan: PlanFree, requested: 0, ok: true},
		{plan: PlanFree, requested: 4, ok: true},
		{plan: PlanFree, requested: 5},
		{plan: PlanScale, requested: 80, ok: true},
		{plan: PlanScale, requested: 81},
		{plan: PlanPro, requested: -1},
		{plan: Plan("unknown"), requested: 1},
	} {
		problem := ValidatePerInstanceConcurrency(tc.plan, tc.requested)
		if tc.ok {
			if problem != nil {
				t.Errorf("%s %d: unexpected problem %+v", tc.plan, tc.requested, problem)
			}
			continue
		}
		if problem == nil || problem.Status != http.StatusUnprocessableEntity || problem.Code != CodeValidation ||
			problem.Limit == nil || *problem.Limit != int64(tc.plan.ConcurrencyPerVMBound()) ||
			problem.Observed == nil || *problem.Observed != int64(tc.requested) || problem.DocsURL == "" {
			t.Errorf("%s %d: problem = %+v, want a 422 carrying limit, observed value and docs", tc.plan, tc.requested, problem)
		}
	}
}

func TestConcurrencyQueueDefaultWaitFor(t *testing.T) {
	for _, tc := range []struct {
		plan        Plan
		perInstance int
		want        time.Duration
	}{
		{plan: PlanFree, want: ConcurrencyQueueFreeDefaultWait},
		{plan: PlanScale, want: ConcurrencyQueuePaidDefaultWait},
		{plan: PlanFree, perInstance: 1, want: PerInstanceConcurrencyQueueDefaultWait},
		{plan: PlanScale, perInstance: 2, want: PerInstanceConcurrencyQueueDefaultWait},
	} {
		if got := ConcurrencyQueueDefaultWaitFor(tc.plan, tc.perInstance); got != tc.want {
			t.Errorf("%s per-instance %d: wait %v, want %v", tc.plan, tc.perInstance, got, tc.want)
		}
	}
}

func TestPerInstanceConcurrencyEnabled(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{env: "", want: false},
		{env: "acct-2", want: false},
		{env: "acct-2, acct-1", want: true},
		{env: " * ", want: true},
	} {
		t.Setenv("FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS", tc.env)
		if got := PerInstanceConcurrencyEnabled("acct-1"); got != tc.want {
			t.Errorf("env %q: enabled = %v, want %v", tc.env, got, tc.want)
		}
	}
}
