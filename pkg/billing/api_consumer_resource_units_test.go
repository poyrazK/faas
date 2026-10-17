package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1308 — compute and response-size cards price each minute in whole
// seconds or megabytes rounded half up; request cards leave minutes alone.
func TestMeterAPIConsumerUsage(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cards := []state.APIConsumerRateCard{
		{ID: "requests", Unit: state.APIConsumerRateCardUnitRequest, EffectiveFrom: start},
		{ID: "compute", Unit: state.APIConsumerRateCardUnitComputeSecond, EffectiveFrom: start.Add(time.Hour)},
		{ID: "bytes", Unit: state.APIConsumerRateCardUnitResponseMegabyte, EffectiveFrom: start.Add(2 * time.Hour)},
	}
	usage := []state.APIConsumerUsageBucket{
		{WindowStart: start.Add(time.Minute), BillableUnits: 3, ComputeMS: 9000},
		{WindowStart: start.Add(time.Hour), BillableUnits: 3, ComputeMS: 1499},
		{WindowStart: start.Add(time.Hour + time.Minute), BillableUnits: 3, ComputeMS: 1500},
		{WindowStart: start.Add(2 * time.Hour), BillableUnits: 3, ResponseBytes: 2_499_999},
		{WindowStart: start.Add(2*time.Hour + time.Minute), BillableUnits: 3, ResponseBytes: 400_000},
	}
	got := MeterAPIConsumerUsage(cards, usage)
	for i, want := range []int64{3, 1, 2, 2, 0} {
		if got[i].BillableUnits != want {
			t.Fatalf("minute %d units = %d, want %d (%+v)", i, got[i].BillableUnits, want, got[i])
		}
	}
	if usage[1].BillableUnits != 3 {
		t.Fatal("MeterAPIConsumerUsage mutated its input")
	}
}
