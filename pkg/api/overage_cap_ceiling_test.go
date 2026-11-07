package api

import "testing"

// A paid subject's self-service spend cap is at most Hobby $75, Pro $200, Scale $1,000;
// Free has no ceiling and may clear its cap.
// adr: 20261105120000
func TestOverageCapProblem(t *testing.T) {
	cents := func(v int64) *int64 { return &v }
	hobbyMax, ok := PlanHobby.MaxSelfServiceOverageCapCents()
	if !ok || hobbyMax != 7_500 {
		t.Fatalf("Hobby ceiling = %d, %v; want 7500 cents ($75)", hobbyMax, ok)
	}
	if v, _ := PlanPro.MaxSelfServiceOverageCapCents(); v != 20_000 {
		t.Fatalf("Pro ceiling = %d, want 20000", v)
	}
	if v, _ := PlanScale.MaxSelfServiceOverageCapCents(); v != 100_000 {
		t.Fatalf("Scale ceiling = %d, want 100000", v)
	}
	if _, ok := PlanFree.MaxSelfServiceOverageCapCents(); ok {
		t.Fatal("Free has a self-service ceiling")
	}
	for _, tc := range []struct {
		name  string
		plan  Plan
		cents *int64
		code  string
	}{
		{"free clears", PlanFree, nil, ""},
		{"free any value", PlanFree, cents(1_000_000), ""},
		{"negative", PlanPro, cents(-1), CodeValidation},
		{"paid clears", PlanHobby, nil, CodeOverageCapRequired},
		{"paid at ceiling", PlanHobby, cents(hobbyMax), ""},
		{"paid above ceiling", PlanHobby, cents(hobbyMax + 1), CodeOverageCapAboveMax},
		{"paid zero", PlanScale, cents(0), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := OverageCapProblem(tc.plan, tc.cents)
			got := ""
			if p != nil {
				got = p.Code
			}
			if got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}
