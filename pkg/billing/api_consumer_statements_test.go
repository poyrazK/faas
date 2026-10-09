package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIConsumerUsageDeltaSubtractsOnlyFinalizedCoverage(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	usage := []state.APIConsumerUsageBucket{
		{WindowStart: start, BillableUnits: 5},
		{WindowStart: start.Add(time.Minute), BillableUnits: 2},
		{WindowStart: start.Add(2 * time.Minute), BillableUnits: 4},
	}
	revisions := []state.APIConsumerUsageStatement{
		{Status: state.APIConsumerUsageStatementSuperseded, Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 1}}},
		{Status: state.APIConsumerUsageStatementFinalized, Buckets: []state.APIConsumerUsageStatementBucket{
			{WindowStart: start, BillableUnits: 3}, {WindowStart: start.Add(time.Minute), BillableUnits: 2},
		}},
		{Status: state.APIConsumerUsageStatementDraft, Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start.Add(2 * time.Minute), BillableUnits: 4}}},
	}
	delta, err := APIConsumerUsageDelta(usage, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 2 || !delta[0].WindowStart.Equal(start) || delta[0].BillableUnits != 2 ||
		!delta[1].WindowStart.Equal(start.Add(2*time.Minute)) || delta[1].BillableUnits != 4 {
		t.Fatalf("delta = %+v, want 2 units at minute 0 and 4 at minute 2", delta)
	}
	if usage[0].BillableUnits != 5 {
		t.Fatalf("caller usage mutated: %+v", usage[0])
	}
}

func TestAPIConsumerUsageDeltaRejectsRegressedUsage(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	finalized := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 3}}}}
	for name, usage := range map[string][]state.APIConsumerUsageBucket{
		"fewer units":    {{WindowStart: start, BillableUnits: 2}},
		"minute missing": {{WindowStart: start.Add(time.Minute), BillableUnits: 9}},
	} {
		if _, err := APIConsumerUsageDelta(usage, finalized); !errors.Is(err, ErrAPIConsumerUsageRegressed) {
			t.Errorf("%s: err = %v, want ErrAPIConsumerUsageRegressed", name, err)
		}
	}
}
