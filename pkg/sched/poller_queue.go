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
//
// These triggers own production rows only. Environment-owned rows and owned
// stage pins are excluded before batching and on every claim/callback. Stage
// delayed tasks remain with the quota-aware generic drain.

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
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
	ops    *wire.OpsMetrics

	// mu protects itemsInFlight — the dispatcher passes item
	// identifiers to Ack/Nack and we record them here so a
	// subsequent Poll sees consistent state while the durable row
	// transition is committed.
	mu            sync.Mutex
	itemsInFlight map[string]queueDeliveryClaim
	owner         *queuePoller
}

// Attempt alone is reusable after an operator replay. The generation keeps
// every acknowledgement tied to the delivery that this poller actually read.
type queueDeliveryClaim struct {
	Attempt          int
	ReplayGeneration int64
}

// Each dispatch gets an immutable view of its delivery handles. Polling can
// reclaim an expired lease without changing the fence used by an older callback.
func (q *queuePoller) deliveryPoller(records []SourceRecord) *queuePoller {
	owner := q
	if q.owner != nil {
		owner = q.owner
	}
	delivery := &queuePoller{pool: q.pool, source: q.source, ops: q.ops, owner: owner, itemsInFlight: make(map[string]queueDeliveryClaim, len(records))}
	for _, r := range records {
		if r.InvocationID == r.ItemIdentifier && r.InvocationAttempt > 0 && r.InvocationReplayGeneration >= 0 {
			delivery.itemsInFlight[r.ItemIdentifier] = queueDeliveryClaim{Attempt: r.InvocationAttempt, ReplayGeneration: r.InvocationReplayGeneration}
		}
	}
	return delivery
}

func (q *queuePoller) forgetClaims(ids []string) {
	q.mu.Lock()
	claims := make(map[string]queueDeliveryClaim, len(ids))
	for _, id := range ids {
		if claim, ok := q.itemsInFlight[id]; ok {
			claims[id] = claim
			delete(q.itemsInFlight, id)
		}
	}
	q.mu.Unlock()
	if q.owner == nil {
		return
	}
	q.owner.mu.Lock()
	defer q.owner.mu.Unlock()
	for id, claim := range claims {
		if q.owner.itemsInFlight[id] == claim {
			delete(q.owner.itemsInFlight, id)
		}
	}
}

// newQueuePoller constructs the queue poller for a kind=queue
// trigger. Returns an error when the trigger's source field is
// missing or set to anything other than 'queue' / 'delayed_task' —
// the SQL trigger.kind='queue' CHECK permits both, but the poller
// needs to know which partition it belongs to.
func newQueuePoller(pool *pgxpool.Pool, t sqlc.Trigger, ops *wire.OpsMetrics) (triggerSource, error) {
	if !t.Source.Valid {
		return nil, fmt.Errorf("poller_queue: trigger missing source")
	}
	if t.Source.String != "queue" && t.Source.String != "delayed_task" {
		return nil, fmt.Errorf("poller_queue: unsupported source %q", t.Source.String)
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(t.Config, &config) == nil {
		if _, marked := config["queue_binding_id"]; marked && !t.QueueBindingID.Valid {
			return nil, fmt.Errorf("poller_queue: queue consumer binding identity requires review")
		}
	}
	q := &queuePoller{
		pool:          pool,
		source:        t.Source.String,
		ops:           ops,
		itemsInFlight: map[string]queueDeliveryClaim{},
	}
	return q, nil
}

// Kind returns the trigger kind this poller handles. The
// dispatcher pairs triggers with pollers by matching on this value.
func (q *queuePoller) Kind() string { return "queue" }

// Poll reads the next batch of `invocations` rows whose (app_id,
// source) matches this trigger, ordered by created_at ASC. The
// named queue poller caps claims at batch_size_max so a record excluded by
// closeBatch does not hold a work-lane or fairness slot. The delayed-task
// compatibility poller retains the older 256-row maximum.
//
// Returned SourceRecord.ItemIdentifier is the invocation id (a
// UUID); the dispatch tick uses it for the durable Ack/Nack transition.
func (q *queuePoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	if q.source == "queue" && t.Slug != "" {
		return q.pollNamedQueue(ctx, t)
	}
	return q.pollLegacyQueue(ctx, t)
}

// A named queue may mix keyed and ordinary rows in one trigger batch. Read
// candidates without row locks, then claim each through the store so keyed
// rows acquire the same lane/fairness locks as the generic invocation drain.
// ClaimQueueTriggerInvocation also serializes the queue binding cap.
func (q *queuePoller) pollNamedQueue(ctx context.Context, t sqlc.Trigger) PollResult {
	pollLimit := 256
	if t.BatchSizeMax > 0 && int(t.BatchSizeMax) < pollLimit {
		pollLimit = int(t.BatchSizeMax)
	}
	const candidateLimit = 1024
	ids, err := sqlc.New().QueuePollCandidates(ctx, q.pool, sqlc.QueuePollCandidatesParams{
		AppID: t.AppID, TriggerID: t.ID, QueueName: t.Slug, CandidateLimit: candidateLimit,
	})
	if err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: list named candidates: %w", err)}
	}
	store := state.NewPgStore(q.pool)
	out := make([]SourceRecord, 0, min(pollLimit, len(ids)))
	claimedAttempts := make(map[string]queueDeliveryClaim, min(pollLimit, len(ids)))
	totalPayloadBytes := 0
	for _, id := range ids {
		claimed, err := store.ClaimQueueTriggerInvocation(ctx, id, t.ID.String(), t.AppID.String(), t.Slug, 600)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			continue
		}
		if errors.Is(err, state.ErrQuotaExceeded) {
			if q.ops != nil {
				q.ops.ObserveQueueBindingConcurrencyThrottled(t.AppID.String(), t.Slug)
			}
			break
		}
		if err != nil {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			releaseErr := q.releaseNamedClaims(releaseCtx, claimedAttempts, t)
			cancel()
			if releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
			return PollResult{Error: fmt.Errorf("poller_queue: claim named candidate %s: %w", id, err)}
		}
		if t.PayloadMaxBytes > 0 && totalPayloadBytes+len(claimed.Payload) > int(t.PayloadMaxBytes) && len(out) > 0 {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			releaseErr := q.releaseNamedClaims(releaseCtx, map[string]queueDeliveryClaim{claimed.ID: {Attempt: claimed.Attempts, ReplayGeneration: claimed.ReplayGeneration}}, t)
			cancel()
			if releaseErr != nil {
				return PollResult{Error: releaseErr}
			}
			break
		}
		out = append(out, SourceRecord{
			ItemIdentifier: claimed.ID, Payload: claimed.Payload,
			InvocationID: claimed.ID, InvocationAttempt: claimed.Attempts, InvocationReplayGeneration: claimed.ReplayGeneration,
			Headers:  parseJSONHeaders(string(claimed.Headers)),
			Metadata: map[string]any{}, ReceivedAt: claimed.CreatedAt,
		})
		claimedAttempts[claimed.ID] = queueDeliveryClaim{Attempt: claimed.Attempts, ReplayGeneration: claimed.ReplayGeneration}
		totalPayloadBytes += len(claimed.Payload)
		if len(out) == pollLimit {
			break
		}
	}
	q.mu.Lock()
	for _, record := range out {
		q.itemsInFlight[record.ItemIdentifier] = claimedAttempts[record.ItemIdentifier]
	}
	q.mu.Unlock()
	return PollResult{Records: out}
}

// Poll has not handed these claims to the dispatcher yet. Release a partial
// batch on a later claim error so those rows can be offered again promptly.
func (q *queuePoller) releaseNamedClaims(ctx context.Context, claims map[string]queueDeliveryClaim, t sqlc.Trigger) error {
	if len(claims) == 0 {
		return nil
	}
	ids := make([]string, 0, len(claims))
	attempts := make([]int32, 0, len(claims))
	generations := make([]int64, 0, len(claims))
	for id, claim := range claims {
		ids = append(ids, id)
		attempts = append(attempts, int32(claim.Attempt))
		generations = append(generations, claim.ReplayGeneration)
	}
	err := sqlc.New().QueueReleasePendingBatchClaims(ctx, q.pool, sqlc.QueueReleasePendingBatchClaimsParams{
		Ids: ids, Attempts: attempts, ReplayGenerations: generations, AppID: t.AppID, BindingID: t.QueueBindingID, QueueName: t.Slug,
	})
	if err != nil {
		return fmt.Errorf("poller_queue: release partial named claims: %w", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, claim := range claims {
		if q.itemsInFlight[id] == claim {
			delete(q.itemsInFlight, id)
		}
	}
	return nil
}

func (q *queuePoller) pollLegacyQueue(ctx context.Context, t sqlc.Trigger) PollResult {
	const pollLimit = 256
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: begin claim: %w", err)}
	}
	defer func() { _ = tx.Rollback(ctx) }()
	limit := pollLimit
	rows, err := sqlc.New().QueuePollLegacyClaims(ctx, tx, sqlc.QueuePollLegacyClaimsParams{
		TriggerID: t.ID, AppID: t.AppID, Source: q.source, PollLimit: int32(limit), QueueName: t.Slug,
	})
	if err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: query invocations: %w", err)}
	}
	out := make([]SourceRecord, 0, len(rows))
	claimedAttempts := make(map[string]queueDeliveryClaim, len(rows))
	for _, row := range rows {
		out = append(out, SourceRecord{
			ItemIdentifier: row.ID, InvocationID: row.ID, InvocationAttempt: int(row.Attempts), InvocationReplayGeneration: row.ReplayGeneration,
			Payload: []byte(row.Payload), Headers: parseJSONHeaders(row.Headers), Metadata: parseJSONMetadata(row.Metadata), ReceivedAt: row.CreatedAt.Time,
		})
		claimedAttempts[row.ID] = queueDeliveryClaim{Attempt: int(row.Attempts), ReplayGeneration: row.ReplayGeneration}
	}
	if err := tx.Commit(ctx); err != nil {
		return PollResult{Error: fmt.Errorf("poller_queue: commit claim: %w", err)}
	}
	// Track in-flight items so a subsequent Ack/Nack on the same
	// trigger sees a consistent set.
	q.mu.Lock()
	for _, r := range out {
		q.itemsInFlight[r.ItemIdentifier] = claimedAttempts[r.ItemIdentifier]
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
	q.forgetClaims(ids)
	return nil
}

// AckWithDeployment records GitOps queue-serving evidence only after the
// durable invocation and trigger receipt have both reached success. The
// gateway supplies the selected deployment for each successful item.
func (q *queuePoller) AckWithDeployment(ctx context.Context, t sqlc.Trigger, acknowledgements []queueDispatchAcknowledgement) error {
	ids := make([]string, 0, len(acknowledgements))
	for _, acknowledgement := range acknowledgements {
		ids = append(ids, acknowledgement.ItemIdentifier)
	}
	if q.source != "queue" || !t.QueueBindingID.Valid || t.QueueBindingScope == "" || !t.QueueBindingEnvironmentID.Valid {
		return q.Ack(ctx, t, ids)
	}
	claimedIDs, attempts, generations := q.currentClaims(ids)
	if len(claimedIDs) == 0 {
		return nil
	}
	servingAcks := make([]state.EnvironmentWorkloadQueueServingAcknowledgement, 0, len(acknowledgements))
	for _, acknowledgement := range acknowledgements {
		if acknowledgement.DeploymentID == "" {
			continue
		}
		servingAcks = append(servingAcks, state.EnvironmentWorkloadQueueServingAcknowledgement{
			Mode: "push", AppID: t.AppID.String(), Scope: t.QueueBindingScope, BindingID: t.QueueBindingID.String(), TriggerID: t.ID.String(),
			DeploymentID: acknowledgement.DeploymentID, InvocationID: acknowledgement.ItemIdentifier,
		})
	}
	finalized, err := state.NewPgStore(q.pool).CompleteEnvironmentQueueDeliveryClaims(ctx, t.AppID.String(), t.ID.String(), q.source,
		claimedIDs, attempts, generations, servingAcks)
	if err != nil {
		return fmt.Errorf("poller_queue: finalize successful queue invocations: %w", err)
	}
	if q.owner != nil && len(finalized) != len(claimedIDs) {
		return state.ErrNotFound
	}
	q.forgetClaims(ids)
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
	q.forgetClaims(ids)
	return nil
}

// NackTerminal is the queue-specific terminal disposition used when a
// record is rejected before a trigger_records row exists (for example, a
// wake-rate-limit denial). External brokers retain the older Ack-after-DLQ
// behavior, so dispatch_triggers.go discovers this optional capability.
func (q *queuePoller) NackTerminal(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	return q.Nack(ctx, t, ids, reason)
}

func (q *queuePoller) currentClaims(ids []string) ([]string, []int32, []int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	claimedIDs := make([]string, 0, len(ids))
	attempts := make([]int32, 0, len(ids))
	generations := make([]int64, 0, len(ids))
	for _, id := range ids {
		if claim := q.itemsInFlight[id]; claim.Attempt > 0 {
			claimedIDs = append(claimedIDs, id)
			attempts = append(attempts, int32(claim.Attempt))
			generations = append(generations, claim.ReplayGeneration)
		}
	}
	return claimedIDs, attempts, generations
}

func (q *queuePoller) finishInvocations(ctx context.Context, t sqlc.Trigger, ids []string, invocationState, recordState, outcome, lastError, result string) error {
	claimedIDs, attempts, generations := q.currentClaims(ids)
	if len(claimedIDs) == 0 {
		return nil
	}
	finalized, err := sqlc.New().QueueFinishDeliveryClaims(ctx, q.pool, sqlc.QueueFinishDeliveryClaimsParams{
		Ids: claimedIDs, Attempts: attempts, ReplayGenerations: generations, AppID: t.AppID, TriggerID: t.ID,
		Source: q.source, InvocationState: invocationState, RecordState: recordState,
		Outcome: pgtype.Text{String: outcome, Valid: true}, Result: []byte(result), LastError: pgtype.Text{String: lastError, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("poller_queue: finish invocations: %w", err)
	}
	if q.owner != nil && len(finalized) != len(claimedIDs) {
		return state.ErrNotFound
	}
	return nil
}

func (q *queuePoller) retryInvocations(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	claimedIDs, attempts, generations := q.currentClaims(ids)
	if len(claimedIDs) == 0 {
		return nil
	}
	err := sqlc.New().QueueRetryDeliveryClaims(ctx, q.pool, sqlc.QueueRetryDeliveryClaimsParams{
		Ids: claimedIDs, Attempts: attempts, ReplayGenerations: generations, AppID: t.AppID, TriggerID: t.ID,
		Source: q.source, Reason: pgtype.Text{String: reason, Valid: true},
	})
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
