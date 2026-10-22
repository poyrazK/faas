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
}

// ScenarioCapturedPlatformRecord is a captured workflow or realtime call.
type ScenarioCapturedPlatformRecord struct {
	Kind, Target, Name string
	Message            json.RawMessage
	Oversized          bool
	CreatedAt          time.Time
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
		Oversized: row.Oversized, ScheduledAt: optionalTimestamptz(row.ScheduledAt), CreatedAt: row.CreatedAt.Time}, nil
}

func (s *PgStore) ScenarioCapturedPlatformCall(ctx context.Context, accountID, runID, id string) (ScenarioCapturedPlatformRecord, error) {
	if uuid.Validate(id) != nil || uuid.Validate(accountID) != nil {
		return ScenarioCapturedPlatformRecord{}, ErrNotFound
	}
	row, err := sqlc.New().ScenarioCapturedPlatformCallByID(ctx, s.pool, sqlc.ScenarioCapturedPlatformCallByIDParams{ID: id, AccountID: accountID, RunID: runID})
	if err != nil {
		return ScenarioCapturedPlatformRecord{}, scenarioCaptureReadError(err, "platform call")
	}
	return ScenarioCapturedPlatformRecord{Kind: row.Kind, Target: row.Target, Name: row.Name, Message: row.Message, Oversized: row.Oversized, CreatedAt: row.CreatedAt.Time}, nil
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

func (m *MemStore) ScenarioCapturedQueueMessage(ctx context.Context, accountID, runID, id string) (ScenarioCapturedQueueRecord, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioCapturedQueueRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.scenarioQueueCaptures {
		if canonicalMemUUID(row.id) == canonicalMemUUID(id) && sameScenarioAccount(row.accountID, accountID) && row.runID == runID {
			return ScenarioCapturedQueueRecord{Kind: row.msg.Kind, Target: row.target, QueueName: row.msg.QueueName, Schedule: row.msg.Schedule,
				Message: append(json.RawMessage(nil), row.msg.Message...), Oversized: row.msg.Oversized, ScheduledAt: row.msg.ScheduledAt, CreatedAt: row.createdAt}, nil
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
			return ScenarioCapturedPlatformRecord{Kind: row.call.Kind, Target: row.target, Name: row.call.Name,
				Message: append(json.RawMessage(nil), row.call.Message...), Oversized: row.call.Oversized, CreatedAt: row.createdAt}, nil
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
