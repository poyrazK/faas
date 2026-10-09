package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"sort"
	"time"
)

type EventConsumerExecutionHealthStore interface {
	GetEventConsumerExecutionHealth(context.Context, string, string, string, time.Time, time.Time) (api.EventConsumerExecutionHealth, error)
}
type executionHealthRoot struct {
	id       string
	accepted time.Time
	outboxID int64
}

func newConsumerExecutionHealth(app, sub string, since, now time.Time) api.EventConsumerExecutionHealth {
	return api.EventConsumerExecutionHealth{SubscriptionID: canonicalMemUUID(sub), AppID: canonicalMemUUID(app), ObservedAt: now, WindowStart: since, Coverage: "bounded_retained_execution_roots"}
}
func finishConsumerExecutionHealth(out *api.EventConsumerExecutionHealth, latencies []float64) {
	if n := out.SuccessfulAttempts + out.FailedAttempts; n > 0 {
		out.HandlerFailurePct = 100 * float64(out.FailedAttempts) / float64(n)
	}
	out.DeadLetterRatePerSecond = float64(out.WindowDeadLetters) / out.ObservedAt.Sub(out.WindowStart).Seconds()
	sort.Float64s(latencies)
	if n := len(latencies); n > 0 {
		p := float64(n-1) * 0.95
		lo := int(p)
		out.CompletionLatencyP95Seconds = latencies[lo]
		if lo+1 < n {
			out.CompletionLatencyP95Seconds += (latencies[lo+1] - latencies[lo]) * (p - float64(lo))
		}
	}
}
func recordConsumerExecution(out *api.EventConsumerExecutionHealth, state string, attempts int, completed *time.Time, accepted time.Time, latencies *[]float64) {
	out.Executions++
	switch InvocationState(state) {
	case InvocationPending:
		if attempts > 0 {
			out.Retrying++
		} else {
			out.Queued++
		}
	case InvocationDispatching:
		out.Running++
	case InvocationCompleted:
		out.Succeeded++
		if completed != nil && !completed.Before(out.WindowStart) && !completed.After(out.ObservedAt) {
			out.WindowCompletions++
			*latencies = append(*latencies, max(0, completed.Sub(accepted).Seconds()))
		}
	case InvocationFailed:
		out.Failed++
	case InvocationExpired:
		out.Expired++
	case InvocationDeadLetter:
		out.DeadLettered++
	case InvocationCancelled:
		out.Cancelled++
	case InvocationSuperseded:
		out.Superseded++
	default:
		out.Unknown++
	}
}
func executionHealthAccepted(roots map[string]time.Time, id, root, parent string) (time.Time, bool) {
	for _, candidate := range []string{id, root, parent} {
		if at, ok := roots[candidate]; ok {
			return at, true
		}
	}
	return time.Time{}, false
}
func (s *PgStore) GetEventConsumerExecutionHealth(ctx context.Context, account, app, sub string, since, now time.Time) (api.EventConsumerExecutionHealth, error) {
	out := newConsumerExecutionHealth(app, sub, since, now)
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if !validEventHealthWindow(since, now) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	candidates, err := q.EventConsumerExecutionRoots(ctx, tx, sqlc.EventConsumerExecutionRootsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), NowAt: pgtypeFromTime(now), RootLimit: api.EventConsumerExecutionRootsMax + 1})
	if err != nil {
		return out, err
	}
	if len(candidates) > api.EventConsumerExecutionRootsMax {
		out.Truncated = true
		candidates = candidates[:api.EventConsumerExecutionRootsMax]
	}
	roots := map[string]time.Time{}
	ids := []pgtype.UUID{}
	for _, r := range candidates {
		id := PublishedEventInvocationID(r.InvocationAccountID, r.Source, r.EventID, canonicalMemUUID(sub))
		if _, ok := roots[id]; !ok {
			ids = append(ids, mustPgUUID(id))
			roots[id] = timeFromPgtype(r.AcceptedAt)
		}
	}
	if len(ids) > 0 {
		earliest := now
		for _, at := range roots {
			if at.Before(earliest) {
				earliest = at
			}
		}
		cancellations, err := q.EventReceiptCancellations(ctx, tx, sqlc.EventReceiptCancellationsParams{AccountID: mustPgUUID(account), InvocationIds: ids, AcceptedAt: pgtypeFromTime(earliest)})
		if err != nil {
			return out, err
		}
		for _, c := range cancellations {
			id := uuidString(c.ID)
			at, ok := roots[id]
			created := timeFromPgtype(c.CreatedAt)
			if ok && sameMemUUID(uuidString(c.AppID), app) && !created.Before(at) && !created.After(now) {
				delete(roots, id)
			}
		}
		ids = ids[:0]
		for id := range roots {
			ids = append(ids, mustPgUUID(id))
		}
	}
	out.RetainedRoots = int64(len(roots))
	if len(ids) == 0 {
		return out, tx.Commit(ctx)
	}
	executions, err := q.EventConsumerExecutionInvocations(ctx, tx, sqlc.EventConsumerExecutionInvocationsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), Roots: ids, InvocationLimit: api.EventConsumerExecutionInvocationsMax + 1})
	if err != nil {
		return out, err
	}
	if len(executions) > api.EventConsumerExecutionInvocationsMax {
		out.Truncated = true
		executions = executions[:api.EventConsumerExecutionInvocationsMax]
	}
	found := map[string]bool{}
	latencies := []float64{}
	for _, i := range executions {
		id := uuidString(i.ID)
		if _, ok := roots[id]; ok {
			found[id] = true
		}
		at, ok := executionHealthAccepted(roots, id, i.ReplayRoot, i.ReplayParent)
		if ok {
			recordConsumerExecution(&out, i.State, int(i.Attempts), timestamptzToTimePtr(i.CompletedAt), at, &latencies)
		}
	}
	out.MissingRoots = int64(len(roots) - len(found))
	attemptRoots := append([]pgtype.UUID{}, ids...)
	for _, i := range executions {
		attemptRoots = append(attemptRoots, i.ID)
	}
	attempts, err := q.EventConsumerExecutionAttempts(ctx, tx, sqlc.EventConsumerExecutionAttemptsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Roots: attemptRoots, SinceAt: pgtypeFromTime(since), NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, err
	}
	out.SuccessfulAttempts, out.FailedAttempts, out.UnknownAttempts, out.WindowDeadLetters = attempts.SuccessfulAttempts, attempts.FailedAttempts, attempts.UnknownAttempts, attempts.DeadLetterAttempts
	finishConsumerExecutionHealth(&out, latencies)
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEventConsumerExecutionHealth(ctx context.Context, account, app, sub string, since, now time.Time) (api.EventConsumerExecutionHealth, error) {
	out := newConsumerExecutionHealth(app, sub, since, now)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if !validEventHealthWindow(since, now) {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	candidates := []executionHealthRoot{}
	for _, receipt := range m.eventFanout {
		if receipt.CreatedAt.After(now) || !sameMemUUID(eventRoutingAccount(receipt), account) {
			continue
		}
		recipient := PublishedEventRecipient{}
		for _, r := range receipt.RecipientSnapshot {
			if sameMemUUID(r.ID, sub) {
				recipient = r
				break
			}
		}
		progress := receipt.RecipientProgress[recipient.ID].State
		for _, r := range receipt.routingRecipients {
			if sameMemUUID(r.Recipient.ID, sub) {
				recipient = r.Recipient
				progress = r.State
				break
			}
		}
		if !sameMemUUID(recipient.AppID, app) || len(recipient.Workflow) > 0 || recipient.ObjectNotification != nil || progress != PublishedEventRecipientEnqueued {
			continue
		}
		var e eventcontract.Envelope
		if json.Unmarshal(receipt.Payload, &e) != nil {
			continue
		}
		candidates = append(candidates, executionHealthRoot{PublishedEventInvocationID(e.AccountID, e.Source, e.ID, recipient.ID), receipt.CreatedAt, receipt.ID})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].accepted.Equal(candidates[j].accepted) {
			return candidates[i].outboxID > candidates[j].outboxID
		}
		return candidates[i].accepted.After(candidates[j].accepted)
	})
	if len(candidates) > api.EventConsumerExecutionRootsMax {
		out.Truncated = true
		candidates = candidates[:api.EventConsumerExecutionRootsMax]
	}
	roots := map[string]time.Time{}
	for _, r := range candidates {
		roots[r.id] = r.accepted
	}
	for id, at := range roots {
		if c, ok := m.workCancellations[id]; ok && sameMemUUID(c.AppID, app) && !c.CreatedAt.Before(at) && !c.CreatedAt.After(now) {
			delete(roots, id)
		}
	}
	out.RetainedRoots = int64(len(roots))
	executions := []Invocation{}
	for _, i := range m.invocations {
		if !sameMemUUID(i.AccountID, account) || !sameMemUUID(i.AppID, app) || i.CreatedAt.After(now) {
			continue
		}
		if _, ok := executionHealthAccepted(roots, i.ID, i.ReplayRootInvocationID, i.ReplayedFromInvocationID); ok {
			executions = append(executions, i)
		}
	}
	sort.Slice(executions, func(i, j int) bool {
		if executions[i].CreatedAt.Equal(executions[j].CreatedAt) {
			return executions[i].ID > executions[j].ID
		}
		return executions[i].CreatedAt.After(executions[j].CreatedAt)
	})
	if len(executions) > api.EventConsumerExecutionInvocationsMax {
		out.Truncated = true
		executions = executions[:api.EventConsumerExecutionInvocationsMax]
	}
	found := map[string]bool{}
	latencies := []float64{}
	for _, i := range executions {
		if _, ok := roots[i.ID]; ok {
			found[i.ID] = true
		}
		at, _ := executionHealthAccepted(roots, i.ID, i.ReplayRootInvocationID, i.ReplayedFromInvocationID)
		recordConsumerExecution(&out, string(i.State), i.Attempts, i.CompletedAt, at, &latencies)
	}
	out.MissingRoots = int64(len(roots) - len(found))
	historyRoots := map[string]bool{}
	for id := range roots {
		historyRoots[id] = true
	}
	for _, i := range executions {
		historyRoots[i.ID] = true
	}
	for _, h := range m.invocationAttemptHistory {
		if !historyRoots[h.rootID] || !sameMemUUID(h.accountID, account) || !sameMemUUID(h.appID, app) || h.FinishedAt == nil || h.FinishedAt.Before(since) || h.FinishedAt.After(now) || !h.RetainUntil.After(now) {
			continue
		}
		switch h.Outcome {
		case "succeeded":
			out.SuccessfulAttempts++
		case "retry", "failed", "dead_letter":
			out.FailedAttempts++
			if h.Outcome == "dead_letter" {
				out.WindowDeadLetters++
			}
		case "unknown":
			out.UnknownAttempts++
		}
	}
	finishConsumerExecutionHealth(&out, latencies)
	return out, nil
}

var _ EventConsumerExecutionHealthStore = (*PgStore)(nil)
var _ EventConsumerExecutionHealthStore = (*MemStore)(nil)
