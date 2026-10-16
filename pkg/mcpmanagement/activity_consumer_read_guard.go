package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only read tools wrap their results. ACK and retirement keep their durable
// receipts even when another operation changes progress before reply delivery.
type consumerToolReadResult struct {
	value    any
	args     activityConsumerArgs
	binding  activityConsumerBinding
	record   activityConsumerRecord
	snapshot *consumerScopeSnapshot
	sample   *activityConsumerResult
}

func guardConsumerRead(value any, args activityConsumerArgs, b activityConsumerBinding, record activityConsumerRecord) consumerToolReadResult {
	return consumerToolReadResult{value: value, args: args, binding: b, record: record}
}

// Preserve the existing wire format; no guard context or journal data is encoded.
func (r consumerToolReadResult) MarshalJSON() ([]byte, error) { return json.Marshal(r.value) }

func (r consumerToolReadResult) checkToolRead(ctx context.Context, m *management) error {
	if r.snapshot == nil {
		return m.checkConsumerObservation(ctx, r.args, r.binding, r.record)
	}
	fresh, err := m.captureConsumerSnapshot(ctx, r.args, r.binding)
	if err != nil {
		return err
	}
	return checkConsumerSnapshot(*r.snapshot, fresh)
}

func (r consumerToolReadResult) observeToolRead(m *management) {
	if r.sample != nil {
		m.opts.Telemetry.observeConsumer(r.sample.Status, r.sample.Lag)
	}
}

type consumerScopeSnapshot struct {
	records    []activityConsumerRecord
	generation string
	digest     string
}

func (m *management) captureConsumerSnapshot(ctx context.Context, base activityConsumerArgs, b activityConsumerBinding) (consumerScopeSnapshot, error) {
	s := m.opts.planStore
	s.mu.Lock()
	defer s.mu.Unlock()
	records, epoch, err := m.consumerStateLocked(ctx, base, b)
	if err != nil {
		return consumerScopeSnapshot{}, err
	}
	return scopedConsumerSnapshot(records, epoch, b)
}

// The caller owns the journal read/lock. The returned slice is a private copy of
// only the selected immutable scope, bounded by the existing connection capacity.
func scopedConsumerSnapshot(records map[string]activityConsumerRecord, epoch activityConsumerEpoch, b activityConsumerBinding) (consumerScopeSnapshot, error) {
	out := consumerScopeSnapshot{generation: epoch.Generation}
	for _, record := range records {
		target := record.Binding
		target.Consumer = b.Consumer
		if target != b {
			continue
		}
		if len(record.Binding.Consumer) > api.MCPActivityConsumerNameMaxBytes || !activityConsumerName.MatchString(record.Binding.Consumer) {
			return out, invalid("invalid_state", "invalid scoped consumer name")
		}
		out.records = append(out.records, record)
	}
	sort.Slice(out.records, func(i, j int) bool { return out.records[i].Binding.Consumer < out.records[j].Binding.Consumer })
	data, err := json.Marshal(out.records)
	if err != nil {
		return out, invalid("invalid_state", "invalid scoped consumer encoding")
	}
	h := sha256.Sum256(data)
	out.digest = hex.EncodeToString(h[:])
	return out, nil
}

func checkConsumerSnapshot(before, after consumerScopeSnapshot) error {
	if before.generation != after.generation {
		return consumerGenerationConflict()
	}
	if before.digest != after.digest {
		return invalid("checkpoint_conflict", "scoped consumers changed; discard stale observations and restart without after")
	}
	return nil
}
