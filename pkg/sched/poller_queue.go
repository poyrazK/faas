// poller_queue.go — in-platform queue / delayed-task poller
// (issue #757 / ADR-0NN, commit #8 of feat-triggers-mega).
//
// The "queue" trigger kind is the unification of two pre-existing
// surfaces:
//
//   - per-app FIFO queue (source='queue' in invocations)
//   - delayed tasks    (source='delayed_task' in invocations)
//
// Both fan through the unified `invocations` table (drain.go).
// The trigger kind=queue poller bridges that pre-existing queue
// into the new trigger envelope: it reads rows where
// (app_id, source) matches the trigger's (kind='queue', source='queue'
// or 'delayed_task') and exposes them as SourceRecord slices.
//
// Why a poller at all if the rows already live in `invocations`?
// The dispatch tick (commit #14) is the only consumer that needs
// the new batching + ReportBatchItemFailures machinery. We can't
// route every `invocations` row through that machinery (existing
// async_invoke traffic keeps its single-record semantics), so the
// poller is the seam that decides which rows opt in.
//
// Poll claims the invocation row before it is handed to the gateway. Ack
// transitions that claim to completed and Nack returns it to pending (or
// dead-letters it after the trigger's attempt budget). This makes the
// Postgres-backed queue behave like the external brokers: a scheduler crash
// leaves a leased row for the expiry reaper, while a gateway failure is
// redelivered without creating a second invocation.

package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// queuePoller is the kind=queue trigger's broker adapter. It holds
// a pgxpool connection (the schedd is a long-lived daemon; the pool
// is shared with the rest of the sched).
//
// Each Queue trigger gets its own queuePoller instance because the
// Poll query uses the trigger's app_id + source filter — the
// connection state is shared (the pool), but the per-trigger
// bindings live on the instance.
type queuePoller struct {
	pool   *pgxpool.Pool
	source string

	// mu protects itemsInFlight — the dispatcher passes item
	// identifiers to Ack/Nack and we record them here so a
	// subsequent Poll sees consistent state while the durable row
	// transition is committed.
	mu            sync.Mutex
	itemsInFlight map[string]struct{}
}

// newQueuePoller constructs the queue poller for a kind=queue
// trigger. Returns an error when the trigger's source field is
// missing or set to anything other than 'queue' / 'delayed_task' —
// the SQL trigger.kind='queue' CHECK permits both, but the poller
// needs to know which partition it belongs to.
func newQueuePoller(pool *pgxpool.Pool, t sqlc.Trigger) (triggerSource, error) {
	if !t.Source.Valid {
		return nil, fmt.Errorf("poller_queue: trigger missing source")
	}
	if t.Source.String != "queue" && t.Source.String != "delayed_task" {
		return nil, fmt.Errorf("poller_queue: unsupported source %q", t.Source.String)
	}
	q := &queuePoller{
		pool:          pool,
		source:        t.Source.String,
		itemsInFlight: map[string]struct{}{},
	}
	return q, nil
}

// Kind returns the trigger kind this poller handles. The
// dispatcher pairs triggers with pollers by matching on this value.
func (q *queuePoller) Kind() string { return "queue" }

// Poll reads the next batch of `invocations` rows whose (app_id,
// source) matches this trigger, ordered by created_at ASC. The
// dispatcher honours batch_size_max AFTER this returns — we
// always pull up to the broker-natural default of 256 (matches the
// old queue receive batch size from drain.go).
//
// Returned SourceRecord.ItemIdentifier is the invocation id (a
// UUID); the dispatch tick uses it for the durable Ack/Nack transition.
func (q *queuePoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	const pollLimit = 256
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: begin claim: %w", err)}
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`with claimed as (
			select i.id
			  from invocations i
			  left join trigger_records tr
			    on tr.trigger_id = $1
			   and tr.item_identifier = i.id::text
			 where i.app_id = $2
			   and i.source = $3
			   and i.state = 'pending'
			   and i.due_at <= now()
			   and (tr.id is null or (tr.state in ('pending','retry') and tr.next_fire_at <= now()))
			 order by i.created_at asc
			 limit $4
			 for update of i skip locked
		), updated as (
			update invocations i
			   set state = 'dispatching',
			       lease_expires_at = now() + interval '60 seconds',
			       received_at = coalesce(i.received_at, now()),
			       attempts = i.attempts + 1
			  from claimed c
			 where i.id = c.id
			 returning i.id::text, i.payload::text, i.headers::text,
			           '{}'::text, i.created_at
		)
		select id, payload, headers, metadata, created_at
		  from updated
		 order by created_at asc, id asc`,
		t.ID, t.AppID, q.source, pollLimit,
	)
	if err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: query invocations: %w", err)}
	}
	defer rows.Close()
	out := make([]SourceRecord, 0, pollLimit)
	for rows.Next() {
		var (
			idStr     string
			payload   string
			headers   string
			metadata  string
			createdAt pgtype.Timestamptz
		)
		if err := rows.Scan(&idStr, &payload, &headers, &metadata, &createdAt); err != nil {
			return PollResult{Error: fmt.Errorf("poller_queue: scan: %w", err)}
		}
		out = append(out, SourceRecord{
			ItemIdentifier: idStr,
			Payload:        []byte(payload),
			Headers:        parseJSONHeaders(headers),
			Metadata:       parseJSONMetadata(metadata),
			ReceivedAt:     createdAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: rows iter: %w", err)}
	}
	if err := tx.Commit(ctx); err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: commit claim: %w", err)}
	}
	// Track in-flight items so a subsequent Ack/Nack on the same
	// trigger sees a consistent set.
	q.mu.Lock()
	for _, r := range out {
		q.itemsInFlight[r.ItemIdentifier] = struct{}{}
	}
	q.mu.Unlock()
	return PollResult{Records: out}
}

// Ack completes the claimed invocation after the worker gateway reports
// success. This is the durable push-consumer acknowledgement: the
// trigger_records row records the trigger-level audit state while the
// invocations row leaves the generic drain queue.
func (q *queuePoller) Ack(ctx context.Context, t sqlc.Trigger, ids []string) error {
	if err := q.finishInvocations(ctx, t, ids, "completed", "succeeded", "success", "", `{"trigger_dispatch":"succeeded"}`); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		delete(q.itemsInFlight, id)
	}
	return nil
}

// Nack requeues the claimed invocation for a later push attempt, or marks
// it dead_letter when the trigger's attempt budget is exhausted. The
// trigger_records retry FSM remains the source of the exact next-fire time;
// the invocation due_at is a short wake guard to avoid a hot poll loop.
func (q *queuePoller) Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	terminal := reason == triggerReasonPoisonRecord || reason == triggerReasonMaxAttempts || reason == triggerReasonPayloadTooLarge || reason == triggerReasonRateLimited
	if terminal {
		if err := q.finishInvocations(ctx, t, ids, "dead_letter", "dead_letter", "dead_letter", reason, `{"trigger_dispatch":"dead_letter"}`); err != nil {
			return err
		}
	} else if err := q.retryInvocations(ctx, t, ids, reason); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		delete(q.itemsInFlight, id)
	}
	return nil
}

// NackTerminal is the queue-specific terminal disposition used when a
// record is rejected before a trigger_records row exists (for example, a
// wake-rate-limit denial). External brokers retain the older Ack-after-DLQ
// behavior, so dispatch_triggers.go discovers this optional capability.
func (q *queuePoller) NackTerminal(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	return q.Nack(ctx, t, ids, reason)
}

func (q *queuePoller) finishInvocations(ctx context.Context, t sqlc.Trigger, ids []string, invocationState, recordState, outcome, lastError, result string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.pool.Exec(ctx, `
		with finalized_records as (
			update trigger_records
			   set state = $1,
			       attempts = attempts + case when $1 = 'dead_letter' and state <> 'dead_letter' then 1 else 0 end,
			       last_error = case when $1 = 'dead_letter' then nullif($9, '') else last_error end,
			       last_dispatched_at = now()
			 where trigger_id = $2
			   and item_identifier = any($3::text[])
			   and state <> $1
			 returning id
		)
		update invocations
		   set state = $4,
		       outcome = $5,
		       result = $6::jsonb,
		       completed_at = now(),
		       lease_expires_at = null,
		       last_error = $9
		 where id::text = any($3::text[])
		   and app_id = $7
		   and source = $8
		   and state = 'dispatching'`, recordState, t.ID, ids, invocationState, outcome, result, t.AppID, q.source, lastError)
	if err != nil {
		return fmt.Errorf("poller_queue: finish invocations: %w", err)
	}
	return nil
}

func (q *queuePoller) retryInvocations(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.pool.Exec(ctx, `
		update invocations i
		   set state = 'pending',
		       outcome = null,
		       completed_at = null,
		       due_at = coalesce((
		           select tr.next_fire_at
		             from trigger_records tr
		            where tr.trigger_id = $1
		              and tr.item_identifier = i.id::text
		       ), now() + interval '1 second'),
		       lease_expires_at = null,
		       last_error = $4
		 where i.id::text = any($2::text[])
		   and i.app_id = $3
		   and i.source = $5
		   and i.state = 'dispatching'`, t.ID, ids, t.AppID, reason, q.source)
	if err != nil {
		return fmt.Errorf("poller_queue: retry invocations: %w", err)
	}
	return nil
}

// Close releases nothing — the pgxpool is owned by the sched, not
// this poller. Kept for the triggerSource interface contract.
func (q *queuePoller) Close() error {
	return nil
}

// parseJSONHeaders turns a Postgres jsonb-encoded headers blob
// into a map[string]string. The default is empty for NULL
// payloads. A JSON decode error is intentionally swallowed — a
// malformed header is one record's problem, not the batch's.
func parseJSONHeaders(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// parseJSONMetadata same as parseJSONHeaders but the value shape
// is map[string]any.
func parseJSONMetadata(s string) map[string]any {
	if s == "" {
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// Queue pollers receive their pool through Loop.newPollerForTrigger. Keeping
// this dependency on the Loop avoids cross-loop state when multiple schedulers
// share a process in tests or during staged handoff.
