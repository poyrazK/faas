package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 361 — extra egress ports are Pro/Scale only, never forbidden ports,
// and stored in canonical form.
func TestUpdateAppEgressPorts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		plan       api.Plan
		ports      []int
		wantStatus int
		wantCode   string
		wantPorts  []int
	}{
		{name: "free gate", plan: api.PlanFree, ports: []int{5432}, wantStatus: 403, wantCode: api.CodePlanEgressPortsNotAllowed},
		{name: "hobby gate", plan: api.PlanHobby, ports: []int{8883}, wantStatus: 403, wantCode: api.CodePlanEgressPortsNotAllowed},
		// Clearing only narrows egress, so it works on every plan: an account
		// that downgraded from Pro can drop ports it can no longer use.
		{name: "hobby may clear", plan: api.PlanHobby, ports: []int{}, wantStatus: 200, wantPorts: []int{}},
		{name: "base ports only is a clear", plan: api.PlanFree, ports: []int{443, 80}, wantStatus: 200, wantPorts: []int{}},
		{name: "smtp forbidden", plan: api.PlanPro, ports: []int{25}, wantStatus: 400, wantCode: api.CodeInvalidEgressPort},
		{name: "mining port forbidden", plan: api.PlanScale, ports: []int{3333}, wantStatus: 400, wantCode: api.CodeInvalidEgressPort},
		{name: "out of range", plan: api.PlanPro, ports: []int{70000}, wantStatus: 400, wantCode: api.CodeInvalidEgressPort},
		{name: "over pro cap", plan: api.PlanPro, ports: []int{5000, 5001, 5002, 5003, 5004, 5005, 5006, 5007, 5008}, wantStatus: 400, wantCode: api.CodeEgressPortsTooMany},
		{name: "normalized", plan: api.PlanPro, ports: []int{6379, 443, 5432, 6379, 80}, wantStatus: 200, wantPorts: []int{5432, 6379}},
		{name: "clear", plan: api.PlanPro, ports: []int{}, wantStatus: 200, wantPorts: []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, tc.plan)
			mustSeedApp(t, e, "ports")
			ports := tc.ports
			rec := e.do(t, "PATCH", "/v1/apps/ports", api.UpdateAppRequest{EgressPorts: &ports}, nil)
			if tc.wantCode != "" {
				assertProblem(t, rec, tc.wantStatus, tc.wantCode)
				return
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var out api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !slices.Equal(out.EgressPorts, tc.wantPorts) || out.EgressPorts == nil {
				t.Fatalf("egress_ports = %#v, want %#v", out.EgressPorts, tc.wantPorts)
			}
		})
	}
}

// Omitting egress_ports leaves the stored list untouched.
func TestUpdateAppEgressPorts_OmittedKeepsValue(t *testing.T) {
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "ports-keep")
	ports := []int{5432}
	if rec := e.do(t, "PATCH", "/v1/apps/ports-keep", api.UpdateAppRequest{EgressPorts: &ports}, nil); rec.Code != 200 {
		t.Fatalf("set: status %d: %s", rec.Code, rec.Body)
	}
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/ports-keep", api.UpdateAppRequest{MinInstances: &one}, nil)
	if rec.Code != 200 {
		t.Fatalf("unrelated patch: status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.Equal(out.EgressPorts, []int{5432}) {
		t.Fatalf("egress_ports after unrelated patch = %v, want [5432]", out.EgressPorts)
	}
}
