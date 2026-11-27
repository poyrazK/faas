package api

import "testing"

func TestExecutionClassRequiresExplicitOptIn(t *testing.T) {
	for _, plan := range []Plan{PlanHobby, PlanPro, PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			request := CreateExecutionRequest{Runtime: ExecutionRuntimeNode24, Source: "export default () => 1", Limits: &ExecutionLimitRequest{TimeoutMS: 300001}}
			if _, p := request.Resolve(plan); p == nil {
				t.Fatal("standard accepted extended duration")
			}
			request.Limits.Class = ExecutionClassExtended
			if _, p := request.Resolve(plan); p == nil {
				t.Fatal("extended accepted above five minutes")
			}
			request.Limits.TimeoutMS = 300000
			r, p := request.Resolve(plan)
			if p != nil || r.Limits.TimeoutMS != 300000 || r.Limits.Class != ExecutionClassExtended {
				t.Fatalf("extended admission: %+v %v", r.Limits, p)
			}
			request.Limits.TimeoutMS = 0
			r, p = request.Resolve(plan)
			if p != nil || r.Limits.TimeoutMS != 300000 {
				t.Fatalf("extended default: %+v %v", r.Limits, p)
			}
			request.Limits.Class = "unbounded"
			if _, p := request.Resolve(plan); p == nil {
				t.Fatal("unknown class accepted")
			}
		})
	}
}
func TestStandardExecutionDefaultsUnchanged(t *testing.T) {
	for _, plan := range []Plan{PlanHobby, PlanPro, PlanScale} {
		r, p := (CreateExecutionRequest{Runtime: ExecutionRuntimeNode24, Source: "export default () => 1"}).Resolve(plan)
		if p != nil || r.Limits.TimeoutMS != 5000 || r.Limits.Class.Normalized() != ExecutionClassStandard {
			t.Fatalf("standard %+v %v", r, p)
		}
	}
}
