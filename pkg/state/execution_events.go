package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/internal/utf8chunk"
	"github.com/onebox-faas/faas/pkg/api"
)

const executionEventReplayLimit = 1000

func validateExecutionEvent(accountID, executionID string, eventType ExecutionEventType, payload json.RawMessage, at time.Time) error {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(executionID) == "" || at.IsZero() {
		return fmt.Errorf("%w: execution event identity and timestamp are required", ErrExecutionInvalid)
	}
	switch eventType {
	case ExecutionEventStatus, ExecutionEventStdout, ExecutionEventStderr, ExecutionEventTerminal:
	default:
		return fmt.Errorf("%w: unsupported execution event type %q", ErrExecutionInvalid, eventType)
	}
	if len(payload) == 0 || len(payload) > ExecutionEventMaxBytes || !json.Valid(payload) {
		return fmt.Errorf("%w: execution event payload is invalid or too large", ErrExecutionInvalid)
	}
	return nil
}

func cloneExecutionEvent(event ExecutionEvent) ExecutionEvent {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	event.CreatedAt = event.CreatedAt.UTC()
	return event
}

func (m *MemStore) AppendExecutionEvent(_ context.Context, accountID, executionID string, eventType ExecutionEventType, payload json.RawMessage, at time.Time) (ExecutionEvent, error) {
	if err := validateExecutionEvent(accountID, executionID, eventType, payload, at); err != nil {
		return ExecutionEvent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || row.AccountID != accountID {
		return ExecutionEvent{}, ErrNotFound
	}
	m.nextExecutionEventID++
	event := ExecutionEvent{
		ExecutionID: executionID,
		AccountID:   accountID,
		Sequence:    m.nextExecutionEventID,
		Type:        eventType,
		Payload:     append(json.RawMessage(nil), payload...),
		CreatedAt:   at.UTC(),
	}
	m.executionEvents[executionID] = append(m.executionEvents[executionID], event)
	m.pruneExecutionEventsLocked(executionID)
	return cloneExecutionEvent(event), nil
}

func (m *MemStore) appendExecutionEventLocked(accountID, executionID string, eventType ExecutionEventType, payload json.RawMessage, at time.Time) {
	if err := validateExecutionEvent(accountID, executionID, eventType, payload, at); err != nil {
		return
	}
	m.nextExecutionEventID++
	event := ExecutionEvent{ExecutionID: executionID, AccountID: accountID, Sequence: m.nextExecutionEventID, Type: eventType, Payload: append(json.RawMessage(nil), payload...), CreatedAt: at.UTC()}
	m.executionEvents[executionID] = append(m.executionEvents[executionID], event)
	m.pruneExecutionEventsLocked(executionID)
}

func (m *MemStore) pruneExecutionEventsLocked(executionID string) {
	events := m.executionEvents[executionID]
	if len(events) <= executionEventReplayLimit {
		return
	}
	if m.executionEventPrunedThrough == nil {
		m.executionEventPrunedThrough = make(map[string]int64)
	}
	cut := len(events) - executionEventReplayLimit
	m.executionEventPrunedThrough[executionID] = events[cut-1].Sequence
	m.executionEvents[executionID] = append([]ExecutionEvent(nil), events[cut:]...)
}

func (m *MemStore) ListExecutionEvents(_ context.Context, accountID, executionID string, afterSequence int64, limit int) ([]ExecutionEvent, error) {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(executionID) == "" || afterSequence < 0 {
		return nil, ErrExecutionInvalid
	}
	if limit <= 0 || limit > executionEventReplayLimit {
		limit = executionEventReplayLimit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || row.AccountID != accountID {
		return nil, ErrNotFound
	}
	events := m.executionEvents[executionID]
	out := make([]ExecutionEvent, 0, minInt(limit, len(events)))
	if through := m.executionEventPrunedThrough[executionID]; through > afterSequence {
		payload, _ := json.Marshal(map[string]any{"execution_id": executionID, "through_sequence": through})
		out = append(out, ExecutionEvent{ExecutionID: executionID, AccountID: accountID, Sequence: through,
			Type: ExecutionEventGap, Payload: payload, CreatedAt: events[0].CreatedAt})
	}
	for _, event := range events {
		if len(out) == limit {
			break
		}
		if event.Sequence <= afterSequence {
			continue
		}
		out = append(out, cloneExecutionEvent(event))
		if len(out) == limit {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func executionStatusPayload(status api.ExecutionStatus) json.RawMessage {
	payload, _ := json.Marshal(struct {
		Status api.ExecutionStatus `json:"status"`
	}{Status: status})
	return payload
}

func executionOutputPayload(chunk string) json.RawMessage {
	payload, _ := json.Marshal(struct {
		Chunk string `json:"chunk"`
	}{Chunk: chunk})
	return payload
}

func executionTerminalPayload(row Execution) json.RawMessage {
	payload := struct {
		Status          api.ExecutionStatus `json:"status"`
		OutputTruncated bool                `json:"output_truncated"`
		ExitCode        *int                `json:"exit_code,omitempty"`
		FailureCode     *string             `json:"failure_code,omitempty"`
		FailureMessage  *string             `json:"failure_message,omitempty"`
		Usage           api.ExecutionUsage  `json:"usage"`
	}{
		Status: row.Status, OutputTruncated: row.OutputTruncated, ExitCode: row.ExitCode,
		FailureCode: row.FailureCode, FailureMessage: row.FailureMessage, Usage: row.Usage,
	}
	b, _ := json.Marshal(payload)
	return b
}

func appendExecutionOutputEventsLocked(m *MemStore, row Execution, at time.Time) {
	forEachExecutionOutputEvent(row, func(eventType ExecutionEventType, payload json.RawMessage) {
		m.appendExecutionEventLocked(row.AccountID, row.ID, eventType, payload, at)
	})
}

func forEachExecutionOutputEvent(row Execution, fn func(ExecutionEventType, json.RawMessage)) {
	if row.Stdout != "" {
		for start := 0; start < len(row.Stdout); {
			// JSON escaping can expand arbitrary UTF-8 considerably; keep
			// chunks well below the event cap so the encoded envelope stays
			// bounded even for non-ASCII output.
			end := start + utf8chunk.Prefix([]byte(row.Stdout[start:]), ExecutionEventMaxBytes/8, true)
			fn(ExecutionEventStdout, executionOutputPayload(row.Stdout[start:end]))
			start = end
		}
	}
	if row.Stderr != "" {
		for start := 0; start < len(row.Stderr); {
			end := start + utf8chunk.Prefix([]byte(row.Stderr[start:]), ExecutionEventMaxBytes/8, true)
			fn(ExecutionEventStderr, executionOutputPayload(row.Stderr[start:end]))
			start = end
		}
	}
}
