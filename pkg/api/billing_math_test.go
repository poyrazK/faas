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
		{name: "below allowance", plan: PlanHobby, total: 49 * sec, want: 0},
		{name: "at allowance", plan: PlanHobby, total: 50 * sec, want: 0},
		{name: "above allowance", plan: PlanHobby, total: 52 * sec, want: 2 * sec},
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
	total := int64(53)*SecondsPerGBHour + SecondsPerGBHour/2
	if got := OverageCentsForMBSeconds(PlanHobby, total); got != 3 {
		t.Fatalf("OverageCentsForMBSeconds = %d, want 3", got)
	}
	if got := OverageCentsForMBSeconds(PlanHobby, 50*SecondsPerGBHour); got != 0 {
		t.Fatalf("at-allowance overage = %d, want 0", got)
	}
}
