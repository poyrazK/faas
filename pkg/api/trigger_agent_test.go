// adr: 20261027090010
package api

import (
	"strings"
	"testing"
)

func TestValidateTriggerAgent(t *testing.T) {
	cases := []struct {
		name     string
		kind     TriggerKind
		delivery string
		agent    string
		ok       bool
	}{
		{"kafka", TriggerKindKafka, TriggerDeliveryRecord, "triage", true},
		{"default delivery", TriggerKindNATS, "", "triage", true},
		{"redis", TriggerKindRedisStreams, TriggerDeliveryRecord, "a", true},
		{"sqs", TriggerKindSQSCompat, TriggerDeliveryRecord, "order-review", true},
		{"queue", TriggerKindQueue, TriggerDeliveryRecord, "triage", false},
		{"realtime", TriggerKindRealtime, TriggerDeliveryRecord, "triage", false},
		{"cron", TriggerKindCron, TriggerDeliveryRecord, "triage", false},
		{"batch", TriggerKindKafka, TriggerDeliveryBatch, "triage", false},
		{"empty name", TriggerKindKafka, TriggerDeliveryRecord, "", false},
		{"upper name", TriggerKindKafka, TriggerDeliveryRecord, "Triage", false},
		{"long name", TriggerKindKafka, TriggerDeliveryRecord, "a" + strings.Repeat("b", AgentNameMaxLen), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTriggerAgent(tc.kind, tc.delivery, tc.agent); tc.ok != (err == nil) {
				t.Fatalf("ValidateTriggerAgent() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
