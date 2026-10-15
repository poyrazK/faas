package managedrealtime

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRealtimeTriggerBatchSizeRules(t *testing.T) {
	batch, record := api.TriggerDeliveryBatch, api.TriggerDeliveryRecord
	n := func(v int) *int { return &v }
	for _, tc := range []struct {
		name     string
		delivery *string
		size     *int
		ok       bool
	}{
		{"record default", nil, nil, true},
		{"record one", &record, n(1), true},
		{"record many", &record, n(4), false},
		{"unset many", nil, n(4), false},
		{"batch many", &batch, n(16), true},
		{"batch too many", &batch, n(17), false},
	} {
		if p := checkRealtimeTriggerBatchSize(tc.delivery, tc.size); (p == nil) != tc.ok {
			t.Errorf("%s: problem = %+v, want ok=%v", tc.name, p, tc.ok)
		}
	}
	if got := RealtimeTriggerBatchSize(record, n(8)); got != 1 {
		t.Fatalf("record batch size = %d, want 1", got)
	}
	if got := RealtimeTriggerBatchSize(batch, nil); got != api.RealtimeTriggerDefaultBatchSize {
		t.Fatalf("batch default = %d", got)
	}
	if got := RealtimeTriggerBatchSize(batch, n(5)); got != 5 {
		t.Fatalf("batch requested = %d", got)
	}
	// A PATCH that keeps batch delivery may raise the size; switching to
	// record delivery must also bring the size back to one.
	if p := CheckRealtimeTriggerUpdate([]byte(`{}`), batch, 8, &api.UpdateTriggerRequest{BatchSizeMax: n(12)}); p != nil {
		t.Fatalf("batch resize = %+v", p)
	}
	if p := CheckRealtimeTriggerUpdate([]byte(`{}`), batch, 8, &api.UpdateTriggerRequest{Delivery: &record}); p == nil {
		t.Fatal("switching to record delivery with 8 messages per invocation was accepted")
	}
	if p := CheckRealtimeTriggerUpdate([]byte(`{}`), batch, 8, &api.UpdateTriggerRequest{Delivery: &record, BatchSizeMax: n(1)}); p != nil {
		t.Fatalf("switch to record with size 1 = %+v", p)
	}
}
