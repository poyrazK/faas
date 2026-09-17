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
// Ack completes the underlying invocation after the trigger record succeeds.
// Retry Nacks leave it pending; terminal Nacks move it to dead_letter. The
// trigger_records row remains the delivery-attempt FSM while invocations stays
// the customer-visible lifecycle.

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
	// subsequent Poll sees consistent state. The field also leaves
	// room for a future queue-poller variant (e.g. an
	// external Postgres with a separate CDC log) needs to track
	// in-flight items to dedupe.
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
// UUID); the dispatch tick uses it for Ack/Nack bookkeeping.
func (q *queuePoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	const pollLimit = 256
	rows, err := q.pool.Query(ctx,
		`select i.id, i.payload::text, i.headers::text, i.metadata::text,
		        i.created_at
		   from invocations i
		   left join trigger_records tr
		     on tr.trigger_id = $1
		    and tr.item_identifier = i.id::text
		  where i.app_id = $2
		    and i.source = $3
		    and i.state = 'pending'
		    and (tr.id is null or (tr.state in ('pending','retry') and tr.next_fire_at <= now()))
		  order by i.created_at asc
		  limit $4`,
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
	// Track in-flight items so a subsequent Ack/Nack on the same
	// trigger sees a consistent set.
	q.mu.Lock()
	for _, r := range out {
		q.itemsInFlight[r.ItemIdentifier] = struct{}{}
	}
	q.mu.Unlock()
	return PollResult{Records: out}
}

// Ack completes the customer-visible invocation after successful dispatch and
// removes it from the poller's in-flight set.
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

// Nack leaves transient broker errors pending for the trigger retry FSM. A
// terminal dispatch failure moves the customer-visible invocation to the
// dead-letter state.
func (q *queuePoller) Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	if reason != triggerReasonBrokerError {
		if err := q.finishInvocations(ctx, t, ids, "dead_letter", "dead_letter", "dead_letter", reason, `{"trigger_dispatch":"dead_letter"}`); err != nil {
			return err
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		delete(q.itemsInFlight, id)
	}
	return nil
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
		       completed_at = now()
		 where id::text = any($3::text[])
		   and app_id = $7
		   and source = $8
		   and state = 'pending'`, recordState, t.ID, ids, invocationState, outcome, result, t.AppID, q.source, lastError)
	if err != nil {
		return fmt.Errorf("poller_queue: finish invocations: %w", err)
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

// newQueuePoller is called directly by Loop.newPollerForTrigger. Queue
// triggers are the only poller kind that needs schedd's database pool, so
// keeping the dependency on Loop avoids the former process-global startup
// side channel and makes multiple Loop instances safe in one process.
