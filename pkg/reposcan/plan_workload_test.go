package reposcan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanWorkloadPortsContract(t *testing.T) {
	for _, ports := range [][]int{nil, {}, {8080}} {
		workload := Workload{Name: "api", Ports: ports}
		plan := PlanWorkload(workload)
		encoded, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		if len(ports) == 0 && !strings.Contains(string(encoded), `"ports":[]`) {
			t.Fatalf("empty ports must remain an array: %s", encoded)
		}
		if len(ports) > 0 {
			if len(plan.Ports) != 1 || plan.Ports[0] != ports[0] {
				t.Fatalf("ports changed: got %v, want %v", plan.Ports, ports)
			}
			plan.Ports[0] = 9090
			if workload.Ports[0] != 8080 {
				t.Fatal("response mutation changed scanner evidence")
			}
		}
	}
}
