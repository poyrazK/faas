// adr: 20261022082721
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioCapturedQueueRecord is a captured queue send, delayed task or
// asynchronous invoke, as the run-local platform API reads it back.
type ScenarioCapturedQueueRecord struct {
	Kind, Target, QueueName, Schedule string
	Message                           json.RawMessage
	Oversized                         bool
	ScheduledAt                       *time.Time
	CreatedAt                         time.Time
	// Delivery is a same-run delivery candidate's result; DeliveryState is
	// the delivered invocation's current state (ADR-20261023111825).
	Delivery, DeliveryState string
}

// ScenarioCapturedPlatformRecord is a captured workflow, realtime or Run
// call. CancelledAt is set on a captured Run start the run later cancelled.
type ScenarioCapturedPlatformRecord struct {
	Kind, Target, Name string
	Message            json.RawMessage
	Oversized          bool
	CreatedAt          time.Time
	CancelledAt        *time.Time
}

// ScenarioCapturedExecution is one of a run's captured Run starts.
type ScenarioCapturedExecution struct {
	ID     string
	Record ScenarioCapturedPlatformRecord
}

// ScenarioCapturedEventRecord is a captured event, found by its envelope
// identity.
type ScenarioCapturedEventRecord struct {
	Type, SchemaVersion string
	CreatedAt           time.Time
}

// ScenarioCapturedReadStore reads a run's own captures back for the run-local
// platform API (ADR-20261022082721). A capture of another run, or a missing
// one, is ErrNotFound.
type ScenarioCapturedReadStore interface {
	ScenarioCapturedQueueMessage(ctx context.Context, accountID, runID, id string) (ScenarioCapturedQueueRecord, error)
	ScenarioCapturedPlatformCall(ctx context.Context, accountID, runID, id string) (ScenarioCapturedPlatformRecord, error)
	ScenarioCapturedEvent(ctx context.Context, accountID, runID, source, eventID string) (ScenarioCapturedEventRecord, error)
	// ScenarioCapturedExecutions lists the run's captured Run starts, newest
	// first (ADR-20261022170348).
	ScenarioCapturedExecutions(ctx context.Context, accountID, runID string) ([]ScenarioCapturedExecution, error)
}

func scenarioCaptureReadError(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read captured rehearsal %s: %w", what, err)
}

func (s *PgStore) ScenarioCapturedQueueMessage(ctx context.Context, accountID, runID, id string) (ScenarioCapturedQueueRecord, error) {
	if uuid.Validate(id) != nil || uuid.Validate(accountID) != nil {
		return ScenarioCapturedQueueRecord{}, ErrNotFound
	}
	row, err := sqlc.New().ScenarioCapturedQueueMessageByID(ctx, s.pool, sqlc.ScenarioCapturedQueueMessageByIDParams{ID: id, AccountID: accountID, RunID: runID})
	if err != nil {
		return ScenarioCapturedQueueRecord{}, scenarioCaptureReadError(err, "queue work")
	}
	return ScenarioCapturedQueueRecord{Kind: row.Kind, Target: row.Target, QueueName: row.QueueName, Schedule: row.Schedule, Message: row.Message,
		Oversized: row.Oversized, ScheduledAt: optionalTimestamptz(row.ScheduledAt), CreatedAt: row.CreatedAt.Time, Delivery: row.Delivery, DeliveryState: row.DeliveryState}, nil
}

func (s *PgStore) ScenarioCapturedPlatformCall(ctx context.Context, accountID, runID, id string) (ScenarioCapturedPlatformRecord, error) {
	if uuid.Validate(id) != nil || uuid.Validate(accountID) != nil {
		return ScenarioCapturedPlatformRecord{}, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.ScenarioCapturedPlatformCallByID(ctx, s.pool, sqlc.ScenarioCapturedPlatformCallByIDParams{ID: id, AccountID: accountID, RunID: runID})
	if err != nil {
		return ScenarioCapturedPlatformRecord{}, scenarioCaptureReadError(err, "platform call")
	}
	record := ScenarioCapturedPlatformRecord{Kind: row.Kind, Target: row.Target, Name: row.Name, Message: row.Message, Oversized: row.Oversized, CreatedAt: row.CreatedAt.Time}
	if row.Kind != api.ScenarioPlatformKindExecution {
		return record, nil
	}
	reference, err := q.ScenarioCapturedExecutionReference(ctx, s.pool, sqlc.ScenarioCapturedExecutionReferenceParams{ID: id, AccountID: accountID, RunID: runID})
	if err != nil {
		return ScenarioCapturedPlatformRecord{}, scenarioCaptureReadError(err, "execution")
	}
	cancelled, err := q.ScenarioCapturedExecutionCancelledAt(ctx, s.pool, sqlc.ScenarioCapturedExecutionCancelledAtParams{AccountID: accountID, RunID: runID, Target: reference})
	if err != nil {
		return ScenarioCapturedPlatformRecord{}, scenarioCaptureReadError(err, "execution cancellation")
	}
	record.CancelledAt = optionalTimestamptz(cancelled)
	return record, nil
}

func (s *PgStore) ScenarioCapturedEvent(ctx context.Context, accountID, runID, source, eventID string) (ScenarioCapturedEventRecord, error) {
	if uuid.Validate(accountID) != nil || source == "" || eventID == "" {
		return ScenarioCapturedEventRecord{}, ErrNotFound
	}
	row, err := sqlc.New().ScenarioCapturedEventByIdentity(ctx, s.pool, sqlc.ScenarioCapturedEventByIdentityParams{AccountID: accountID, RunID: runID, Source: source, EventID: eventID})
	if err != nil {
		return ScenarioCapturedEventRecord{}, scenarioCaptureReadError(err, "event")
	}
	return ScenarioCapturedEventRecord{Type: row.Type, SchemaVersion: row.SchemaVersion, CreatedAt: row.CreatedAt.Time}, nil
}

func (s *PgStore) ScenarioCapturedExecutions(ctx context.Context, accountID, runID string) ([]ScenarioCapturedExecution, error) {
	if uuid.Validate(accountID) != nil {
		return nil, nil
	}
	rows, err := sqlc.New().ScenarioCapturedExecutions(ctx, s.pool, sqlc.ScenarioCapturedExecutionsParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("list captured rehearsal executions: %w", err)
	}
	out := make([]ScenarioCapturedExecution, 0, len(rows))
	for _, row := range rows {
		out = append(out, ScenarioCapturedExecution{ID: row.ID, Record: ScenarioCapturedPlatformRecord{Kind: api.ScenarioPlatformKindExecution, Target: api.ScenarioExecutionTarget,
			Name: row.Name, Message: row.Message, Oversized: row.Oversized, CreatedAt: row.CreatedAt.Time, CancelledAt: optionalTimestamptz(row.CancelledAt)}})
	}
	return out, nil
}

func (m *MemStore) ScenarioCapturedQueueMessage(ctx context.Context, accountID, runID, id string) (ScenarioCapturedQueueRecord, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioCapturedQueueRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.scenarioQueueCaptures {
		if canonicalMemUUID(row.id) == canonicalMemUUID(id) && sameScenarioAccount(row.accountID, accountID) && row.runID == runID {
			record := ScenarioCapturedQueueRecord{Kind: row.msg.Kind, Target: row.target, QueueName: row.msg.QueueName, Schedule: row.msg.Schedule,
				Message: append(json.RawMessage(nil), row.msg.Message...), Oversized: row.msg.Oversized, ScheduledAt: row.msg.ScheduledAt, CreatedAt: row.createdAt, Delivery: row.delivery}
			if row.delivery == api.ScenarioQueueDelivered {
				record.DeliveryState = string(m.invocations[row.id].State)
			}
			return record, nil
		}
	}
	return ScenarioCapturedQueueRecord{}, ErrNotFound
}

func (m *MemStore) ScenarioCapturedPlatformCall(ctx context.Context, accountID, runID, id string) (ScenarioCapturedPlatformRecord, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioCapturedPlatformRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.scenarioPlatformCalls {
		if canonicalMemUUID(row.id) == canonicalMemUUID(id) && sameScenarioAccount(row.accountID, accountID) && row.runID == runID {
			record := ScenarioCapturedPlatformRecord{Kind: row.call.Kind, Target: row.target, Name: row.call.Name,
				Message: append(json.RawMessage(nil), row.call.Message...), Oversized: row.call.Oversized, CreatedAt: row.createdAt}
			if row.call.Kind == api.ScenarioPlatformKindExecution {
				record.CancelledAt = m.scenarioExecutionCancelledAtLocked(accountID, runID, id)
			}
			return record, nil
		}
	}
	return ScenarioCapturedPlatformRecord{}, ErrNotFound
}

func (m *MemStore) ScenarioCapturedEvent(ctx context.Context, accountID, runID, source, eventID string) (ScenarioCapturedEventRecord, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioCapturedEventRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.scenarioCapturedEvents {
		if eventID != "" && row.event.ID == eventID && row.event.Source == source && sameScenarioAccount(row.accountID, accountID) && row.runID == runID {
			return ScenarioCapturedEventRecord{Type: row.event.Type, SchemaVersion: row.event.SchemaVersion, CreatedAt: row.createdAt}, nil
		}
	}
	return ScenarioCapturedEventRecord{}, ErrNotFound
}

// scenarioExecutionCancelledAtLocked mirrors ScenarioCapturedExecutionCancelledAt.
func (m *MemStore) scenarioExecutionCancelledAtLocked(accountID, runID, id string) *time.Time {
	reference := m.scenarioExecutionReferenceLocked(accountID, runID, id)
	for _, row := range m.scenarioPlatformCalls {
		if sameScenarioAccount(row.accountID, accountID) && row.runID == runID && row.call.Kind == api.ScenarioPlatformKindExecutionCancel && row.target == reference {
			at := row.createdAt
			return &at
		}
	}
	return nil
}

func (m *MemStore) ScenarioCapturedExecutions(ctx context.Context, accountID, runID string) ([]ScenarioCapturedExecution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ScenarioCapturedExecution
	for i := len(m.scenarioPlatformCalls) - 1; i >= 0; i-- {
		row := m.scenarioPlatformCalls[i]
		if row.call.Kind != api.ScenarioPlatformKindExecution || !sameScenarioAccount(row.accountID, accountID) || row.runID != runID {
			continue
		}
		out = append(out, ScenarioCapturedExecution{ID: row.id, Record: ScenarioCapturedPlatformRecord{Kind: row.call.Kind, Target: row.target, Name: row.call.Name,
			Message: append(json.RawMessage(nil), row.call.Message...), Oversized: row.call.Oversized, CreatedAt: row.createdAt,
			CancelledAt: m.scenarioExecutionCancelledAtLocked(accountID, runID, row.id)}})
	}
	return out, nil
}
