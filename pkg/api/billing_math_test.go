package api

import "testing"

func TestBillableMBSecondsUsesPlanAllowance(t *testing.T) {
	const sec = SecondsPerGBHour
	tests := []struct {
		name  string
		plan  Plan
		total int64
		want  int64
	}{
		{name: "below allowance", plan: PlanHobby, total: 199 * sec, want: 0},
		{name: "at allowance", plan: PlanHobby, total: 200 * sec, want: 0},
		{name: "above allowance", plan: PlanHobby, total: 202 * sec, want: 2 * sec},
		{name: "non-positive", plan: PlanPro, total: -1, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BillableMBSeconds(tc.plan, tc.total); got != tc.want {
				t.Fatalf("BillableMBSeconds(%q, %d) = %d, want %d", tc.plan, tc.total, got, tc.want)
			}
		})
	}
}

func TestOverageCentsForMBSecondsUsesCorrectUnits(t *testing.T) {
	// One included Hobby allowance plus three and a half GB-hours is three
	// cents, because the overage price is €0.01 per GB-hour.
	total := int64(203)*SecondsPerGBHour + SecondsPerGBHour/2
	if got := OverageCentsForMBSeconds(PlanHobby, total); got != 3 {
		t.Fatalf("OverageCentsForMBSeconds = %d, want 3", got)
	}
	if got := OverageCentsForMBSeconds(PlanHobby, 200*SecondsPerGBHour); got != 0 {
		t.Fatalf("at-allowance overage = %d, want 0", got)
	}
}

// A paid plan's default spend cap is one more allowance at the overage rate,
// the managed PostgreSQL overage ceiling; Free has none.
// adr: 20261029180000
func TestDefaultOverageCapCents(t *testing.T) {
	for plan, want := range map[Plan]int64{PlanHobby: 200, PlanPro: 1_000, PlanScale: 3_000} {
		got, ok := plan.DefaultOverageCapCents()
		if !ok || got != want {
			t.Errorf("%s default cap = (%d, %v), want (%d, true)", plan, got, ok, want)
		}
		allowance := int64(plan.PlanIncludedGBHours()) * OverageMillicentsPerGBHour / MillicentsPerCent
		if got != allowance*(ManagedPostgresOverageCeilingMultiplier-1) {
			t.Errorf("%s default cap %d is not one allowance (%d cents) of overage", plan, got, allowance)
		}
	}
	if got, ok := PlanFree.DefaultOverageCapCents(); ok {
		t.Errorf("free default cap = %d, want none", got)
	}
}
