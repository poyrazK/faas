package api

import "testing"

func TestValidateTriggerDelivery(t *testing.T) {
	for _, tc := range []struct {
		kind     TriggerKind
		delivery string
		ok       bool
	}{
		{TriggerKindKafka, TriggerDeliveryRecord, true},
		{TriggerKindKafka, TriggerDeliveryBatch, true},
		{TriggerKindRealtime, TriggerDeliveryBatch, true},
		{TriggerKindSQSCompat, TriggerDeliveryBatch, true},
		{TriggerKindCron, TriggerDeliveryRecord, true},
		{TriggerKindCron, TriggerDeliveryBatch, false},
		{TriggerKindQueue, TriggerDeliveryBatch, false},
		{TriggerKindKafka, "stream", false},
		{TriggerKindKafka, "", false},
	} {
		if err := ValidateTriggerDelivery(tc.kind, tc.delivery); (err == nil) != tc.ok {
			t.Errorf("ValidateTriggerDelivery(%s, %q) = %v, want ok=%v", tc.kind, tc.delivery, err, tc.ok)
		}
	}
}
