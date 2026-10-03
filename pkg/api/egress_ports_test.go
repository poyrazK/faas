// adr: 361 — per-app extra egress ports.
package api

import (
	"net/http"
	"reflect"
	"testing"
)

func TestNormalizeEgressPorts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     Plan
		raw      []int
		want     []int
		wantCode string
	}{
		{name: "nil clears", plan: PlanPro, raw: nil, want: []int{}},
		{name: "base ports only are a no-op even on Free", plan: PlanFree, raw: []int{443, 80}, want: []int{}},
		{name: "sorted and deduplicated", plan: PlanPro, raw: []int{6379, 5432, 443, 6379}, want: []int{5432, 6379}},
		{name: "Free cannot declare extra ports", plan: PlanFree, raw: []int{5432}, wantCode: CodePlanEgressPortsNotAllowed},
		{name: "Hobby cannot declare extra ports", plan: PlanHobby, raw: []int{5432}, wantCode: CodePlanEgressPortsNotAllowed},
		{name: "over the Pro cap", plan: PlanPro, raw: []int{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009}, wantCode: CodeEgressPortsTooMany},
		{name: "Scale cap is larger", plan: PlanScale, raw: []int{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009}, want: []int{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009}},
		{name: "zero", plan: PlanPro, raw: []int{0}, wantCode: CodeInvalidEgressPort},
		{name: "above 65535", plan: PlanPro, raw: []int{65536}, wantCode: CodeInvalidEgressPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := NormalizeEgressPorts(tc.plan, tc.raw)
			if tc.wantCode != "" {
				if problem == nil || problem.Code != tc.wantCode {
					t.Fatalf("problem = %+v, want code %s", problem, tc.wantCode)
				}
				return
			}
			if problem != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v %+v, want %v", got, problem, tc.want)
			}
		})
	}
}

// Every forbidden category is refused, even on the plan with the largest cap.
func TestNormalizeEgressPortsRefusesForbiddenPorts(t *testing.T) {
	for _, port := range []int{25, 465, 587, 2525, 22, 23, 3389, 5900, 445, 139, 6667, 6697, 3333, 4444, 14444, 53, 853} {
		_, problem := NormalizeEgressPorts(PlanScale, []int{port})
		if problem == nil || problem.Code != CodeInvalidEgressPort || problem.Status != http.StatusBadRequest {
			t.Errorf("port %d: problem = %+v, want 400 %s", port, problem, CodeInvalidEgressPort)
		}
	}
}

func TestEgressPortsProblemsCarryLimits(t *testing.T) {
	p := ErrEgressPortsTooMany(9, 8)
	if p.Limit == nil || *p.Limit != 8 || p.Observed == nil || *p.Observed != 9 || p.DocsURL == "" {
		t.Fatalf("too-many problem lacks limit/observed/docs: %+v", p)
	}
	if ErrPlanEgressPortsNotAllowed(PlanFree).DocsURL == "" {
		t.Fatal("plan-gate problem has no docs URL")
	}
}
