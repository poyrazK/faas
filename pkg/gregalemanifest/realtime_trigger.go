package gregalemanifest

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// validateRealtime checks a kind=realtime trigger (ADR-1237). The bound
// consumer group owns ordering, filters, retries and dead letters, so the
// trigger-level delivery settings that would compete with it are rejected.
func (t Trigger) validateRealtime(idx int) error {
	var c api.RealtimeTriggerConfig
	if err := decodeInto(t.Config, &c); err != nil {
		return fmt.Errorf("trigger[%d]: bad realtime config: %w", idx, err)
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("trigger[%d]: %w", idx, err)
	}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"batch_window_ms", t.BatchWindowMs != 0},
		{"max_attempts", t.MaxAttempts != 0},
		{"retry_policy", t.RetryPolicy != nil},
		{"broker_poison_strategy", t.BrokerPoisonStrategy != ""},
		{"filter_criteria", t.FilterCriteria != nil},
		{"work_policy", t.WorkPolicy != "" || t.WorkKey != "" || t.WorkFairnessKey != ""},
	} {
		if f.set {
			return fmt.Errorf("trigger[%d]: %s is not supported for kind=realtime; configure it on the consumer group", idx, f.name)
		}
	}
	if t.BatchSizeMax > 1 {
		return fmt.Errorf("trigger[%d]: kind=realtime delivers one message per invocation; batch_size_max must be 1", idx)
	}
	return nil
}
