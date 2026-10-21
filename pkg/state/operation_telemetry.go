// adr: 20261016123221
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OperationTelemetry is one fleet-wide reading of Operation state for the
// schedd gauges. Outcomes cover the last OperationTelemetryWindowSeconds.
type OperationTelemetry struct {
	ObservedAt             time.Time
	States                 map[api.OperationState]OperationStateTelemetry
	Waiting                map[string]int64
	Overdue                int64
	Outcomes               map[OperationOutcomeKey]int64
	OutcomeUnknownAccounts int64
}

// OperationStateTelemetry counts the Operations in one pending state and the
// last change of the least recently changed one.
type OperationStateTelemetry struct {
	Count        int64
	OldestUpdate time.Time
}

// OperationOutcomeKey is an outcome event type and its failure code, as
// recorded; the metric bounds both.
type OperationOutcomeKey struct {
	EventType, FailureCode string
}

type OperationTelemetryStore interface {
	OperationTelemetry(ctx context.Context, now time.Time) (OperationTelemetry, error)
}

// operationOutcomeEvents are the events that settle an Operation or hand it
// to reconciliation.
var operationOutcomeEvents = map[string]bool{"succeeded": true, "failed": true, "cancelled": true, "reconciliation_required": true}

func operationTelemetryBounds(now time.Time) (overdueBefore, since time.Time) {
	return now.Add(-api.OperationDeadlineOverdueSeconds * time.Second), now.Add(-api.OperationTelemetryWindowSeconds * time.Second)
}

func newOperationTelemetry(now time.Time) OperationTelemetry {
	return OperationTelemetry{ObservedAt: now, States: map[api.OperationState]OperationStateTelemetry{}, Waiting: map[string]int64{}, Outcomes: map[OperationOutcomeKey]int64{}}
}

func (t *OperationTelemetry) addPending(state api.OperationState, waitKind string, count int64, oldest time.Time) {
	entry := t.States[state]
	if entry.Count == 0 || oldest.Before(entry.OldestUpdate) {
		entry.OldestUpdate = oldest
	}
	entry.Count += count
	t.States[state] = entry
	if state == api.OperationWaiting && waitKind != "" {
		t.Waiting[waitKind] += count
	}
}

func (s *PgStore) OperationTelemetry(ctx context.Context, now time.Time) (OperationTelemetry, error) {
	q := sqlc.New()
	overdueBefore, since := operationTelemetryBounds(now)
	out := newOperationTelemetry(now)
	pending, err := q.CustomerOperationActiveTelemetry(ctx, s.pool, pgtype.Timestamptz{Time: overdueBefore, Valid: true})
	if err != nil {
		return OperationTelemetry{}, fmt.Errorf("state: operation telemetry: %w", err)
	}
	for _, row := range pending {
		out.addPending(api.OperationState(row.State), row.WaitKind, row.Operations, row.OldestUpdate.Time)
		out.Overdue += row.Overdue
	}
	outcomes, err := q.CustomerOperationOutcomeTelemetry(ctx, s.pool, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return OperationTelemetry{}, fmt.Errorf("state: operation outcome telemetry: %w", err)
	}
	for _, row := range outcomes {
		out.Outcomes[OperationOutcomeKey{row.EventType, row.FailureCode}] += row.Operations
	}
	if out.OutcomeUnknownAccounts, err = q.CustomerOperationOutcomeUnknownAccounts(ctx, s.pool, pgtype.Timestamptz{Time: since, Valid: true}); err != nil {
		return OperationTelemetry{}, fmt.Errorf("state: operation outcome accounts: %w", err)
	}
	return out, nil
}

func (m *MemStore) OperationTelemetry(_ context.Context, now time.Time) (OperationTelemetry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	overdueBefore, since := operationTelemetryBounds(now)
	out := newOperationTelemetry(now)
	data := m.operationMemoryLocked()
	for _, op := range data.operations {
		switch op.State {
		case api.OperationAccepted, api.OperationRunning, api.OperationWaiting:
			if op.DeadlineAt != nil && op.DeadlineAt.Before(overdueBefore) {
				out.Overdue++
			}
		case api.OperationRequiresReconciliation:
		default:
			continue
		}
		kind := ""
		if op.Waiting != nil {
			kind = op.Waiting.Kind
		}
		out.addPending(op.State, kind, 1, op.UpdatedAt)
	}
	unknown := map[string]bool{}
	for id, events := range data.events {
		for _, event := range events {
			if !operationOutcomeEvents[event.Type] || event.CreatedAt.Before(since) {
				continue
			}
			var payload struct {
				FailureCode string `json:"failure_code"`
			}
			_ = json.Unmarshal(event.Data, &payload)
			out.Outcomes[OperationOutcomeKey{event.Type, payload.FailureCode}]++
			if event.Type == "reconciliation_required" && payload.FailureCode == "execution_outcome_unknown" {
				unknown[data.operations[id].AccountID] = true
			}
		}
	}
	out.OutcomeUnknownAccounts = int64(len(unknown))
	return out, nil
}

var (
	_ OperationTelemetryStore = (*PgStore)(nil)
	_ OperationTelemetryStore = (*MemStore)(nil)
)
