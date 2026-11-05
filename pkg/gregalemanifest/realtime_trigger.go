package gregalemanifest

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// validateRealtime checks a kind=realtime trigger (ADR-1322). The bound
// consumer group owns ordering, filters, retries and dead letters, so the
// trigger-level delivery settings that would compete with it are rejected.
func (t Trigger) validateRealtime(idx int) error {
	var c api.RealtimeTriggerConfig
	cfg := t.Config
	// An endpoint declared in this manifest may be named by slug
	// (ADR-20261019102657); the deploy resolves it to its ID, and
	// Manifest.validateRealtime checks that it is declared.
	if ref, ok := t.Config["endpoint"]; ok {
		slug, isString := ref.(string)
		if !isString || api.ValidateRealtimeEndpointSlug(slug) != nil {
			return fmt.Errorf("trigger[%d]: realtime endpoint must be the slug of an endpoint in realtime.endpoints", idx)
		}
		if _, both := t.Config["endpoint_id"]; both {
			return fmt.Errorf("trigger[%d]: set endpoint or endpoint_id, not both", idx)
		}
		cfg = make(map[string]any, len(t.Config))
		for k, v := range t.Config {
			if k != "endpoint" {
				cfg[k] = v
			}
		}
		cfg["endpoint_id"] = "00000000-0000-0000-0000-000000000000"
	}
	if err := decodeInto(cfg, &c); err != nil {
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
	if t.BatchSizeMax > 1 && t.Delivery != api.TriggerDeliveryBatch {
		return fmt.Errorf("trigger[%d]: kind=realtime delivers one message per invocation unless delivery is batch; set delivery: batch or batch_size_max: 1", idx)
	}
	if t.BatchSizeMax > api.RealtimeConsumerGroupBatchMax {
		return fmt.Errorf("trigger[%d]: kind=realtime with delivery: batch receives at most %d messages per invocation", idx, api.RealtimeConsumerGroupBatchMax)
	}
	return nil
}
