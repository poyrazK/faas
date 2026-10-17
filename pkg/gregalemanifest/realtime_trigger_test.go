package gregalemanifest

import (
	"strings"
	"testing"
)

func TestValidateRealtimeTrigger(t *testing.T) {
	base := func() Trigger {
		return Trigger{Kind: TriggerKindRealtime, App: "orders-fn", Slug: "orders", Config: map[string]any{
			"endpoint_id": "6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11", "channel": "orders", "group": "billing",
		}}
	}
	cases := []struct {
		name    string
		mutate  func(*Trigger)
		wantErr string
	}{
		{"valid", func(*Trigger) {}, ""},
		{"batch of one", func(tr *Trigger) { tr.BatchSizeMax = 1 }, ""},
		{"missing group", func(tr *Trigger) { delete(tr.Config, "group") }, "group"},
		{"batch above one", func(tr *Trigger) { tr.BatchSizeMax = 2 }, "set delivery: batch or batch_size_max: 1"},
		{"batch delivery", func(tr *Trigger) { tr.Delivery, tr.BatchSizeMax = "batch", 16 }, ""},
		{"batch delivery too large", func(tr *Trigger) { tr.Delivery, tr.BatchSizeMax = "batch", 17 }, "at most 16 messages"},
		{"unknown delivery", func(tr *Trigger) { tr.Delivery = "stream" }, "delivery must be"},
		{"max attempts", func(tr *Trigger) { tr.MaxAttempts = 3 }, "max_attempts is not supported"},
		{"retry policy", func(tr *Trigger) { tr.RetryPolicy = &RetryPolicyConfig{} }, "retry_policy is not supported"},
		{"filter", func(tr *Trigger) { tr.FilterCriteria = &FilterCriteria{} }, "filter_criteria is not supported"},
		{"work policy", func(tr *Trigger) { tr.WorkPolicy = "p" }, "work_policy is not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := base()
			tc.mutate(&tr)
			m := &Manifest{Triggers: []Trigger{tr}}
			err := m.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
