package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type storedTriggerReplaySource struct {
	records []sqlc.TriggerRecord
	kind    string
}

func (p *storedTriggerReplaySource) Kind() string                                      { return p.kind }
func (p *storedTriggerReplaySource) Close() error                                      { return nil }
func (p *storedTriggerReplaySource) Ack(context.Context, sqlc.Trigger, []string) error { return nil }
func (p *storedTriggerReplaySource) Nack(context.Context, sqlc.Trigger, []string, string) error {
	return nil
}
func (p *storedTriggerReplaySource) Poll(_ context.Context, t sqlc.Trigger) PollResult {
	result := PollResult{}
	records := p.records
	// The records were listed before the per-plan record rate trimmed the
	// batch; hand out only what the budget took. The rest stay due in the
	// store for a later tick.
	if t.BatchSizeMax > 0 && int(t.BatchSizeMax) < len(records) {
		records = records[:t.BatchSizeMax]
	}
	for _, r := range records {
		var headers map[string]string
		var metadata map[string]any
		if len(r.Headers) > 0 && json.Unmarshal(r.Headers, &headers) != nil {
			return PollResult{Error: fmt.Errorf("stored trigger replay headers: %w", state.ErrInvalidArgument)}
		}
		if len(r.Metadata) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(r.Metadata))
			decoder.UseNumber()
			if decoder.Decode(&metadata) != nil {
				return PollResult{Error: fmt.Errorf("stored trigger replay metadata: %w", state.ErrInvalidArgument)}
			}
		}
		result.Records = append(result.Records, SourceRecord{ItemIdentifier: r.ItemIdentifier, StableIdentifier: r.ItemIdentifier, StoredReplay: true, Payload: r.Payload, Headers: headers, Metadata: metadata, ReceivedAt: r.ReceivedAt.Time})
	}
	return result
}
