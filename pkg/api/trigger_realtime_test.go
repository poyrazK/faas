package api

import "testing"

func TestRealtimeTriggerConfigValidate(t *testing.T) {
	valid := RealtimeTriggerConfig{EndpointID: "6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11", Channel: "orders", Group: "billing"}
	cases := []struct {
		name   string
		mutate func(*RealtimeTriggerConfig)
		ok     bool
	}{
		{"valid", func(*RealtimeTriggerConfig) {}, true},
		{"endpoint not a uuid", func(c *RealtimeTriggerConfig) { c.EndpointID = "ep" }, false},
		{"missing channel", func(c *RealtimeTriggerConfig) { c.Channel = "" }, false},
		{"missing group", func(c *RealtimeTriggerConfig) { c.Group = "" }, false},
		{"group too long", func(c *RealtimeTriggerConfig) { c.Group = string(make([]byte, RealtimeConsumerGroupNameMaxBytes+1)) }, false},
		{"negative concurrency", func(c *RealtimeTriggerConfig) { c.MaxConcurrency = -1 }, false},
		{"concurrency above node slots", func(c *RealtimeTriggerConfig) { c.MaxConcurrency = QueuePushDispatchSlotsPerNode + 1 }, false},
		{"concurrency at node slots", func(c *RealtimeTriggerConfig) { c.MaxConcurrency = QueuePushDispatchSlotsPerNode }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	if got := (RealtimeTriggerConfig{}).Concurrency(); got != 1 {
		t.Fatalf("default concurrency = %d, want 1", got)
	}
}
