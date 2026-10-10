package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Execution selection shares the durable job budget, never routing checkpoints.
func eventRecoveryCoverage(req api.EventRecoveryRequest) string {
	if req.Mode == "execution" {
		return "retained_application_executions"
	}
	return EventRecoveryCoverage
}

type executionRecoveryIdentity struct {
	ParentJobID    string `json:"parent_job_id,omitempty"`
	ParentPosition int64  `json:"parent_position,omitempty"`

	InvocationID string          `json:"invocation_id"`
	State        InvocationState `json:"state"`
	Attempts     int             `json:"attempts"`
	Generation   int64           `json:"generation"`
	CreatedAt    time.Time       `json:"created_at"`
	CompletedAt  *time.Time      `json:"completed_at"`
	DeadLetterID string          `json:"dead_letter_id"`
}

func executionRecoveryInvocationID(encoded []byte) string {
	var identity executionRecoveryIdentity
	_ = json.Unmarshal(encoded, &identity)
	return identity.InvocationID
}
func executionRecoveryIdentityFor(inv Invocation, deadLetterID string) executionRecoveryIdentity {
	return executionRecoveryIdentity{InvocationID: inv.ID, State: inv.State, Attempts: inv.Attempts, Generation: inv.ReplayGeneration, CreatedAt: inv.CreatedAt, CompletedAt: inv.CompletedAt, DeadLetterID: deadLetterID}
}
func (identity executionRecoveryIdentity) matches(inv Invocation) bool {
	if identity.ParentJobID != "" && inv.Outcome != nil && *inv.Outcome == OutcomeUncertain {
		return false
	}
	if identity.InvocationID != inv.ID || identity.State != inv.State || identity.Attempts != inv.Attempts || identity.Generation != inv.ReplayGeneration || !identity.CreatedAt.Equal(inv.CreatedAt) {
		return false
	}
	return identity.CompletedAt == nil && inv.CompletedAt == nil || identity.CompletedAt != nil && inv.CompletedAt != nil && identity.CompletedAt.Equal(*inv.CompletedAt)
}

type executionRecoveryRoot struct {
	OutboxID       int64  `json:"outbox_id"`
	EventSource    string `json:"event_source"`
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	SubscriptionID string `json:"subscription_id"`
	RootID         string `json:"root_id"`
}

func eventExecutionRecoveryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, req api.EventRecoveryRequest, now time.Time) ([]sqlc.EventRecoveryCandidatesRow, error) {
	out := []sqlc.EventRecoveryCandidatesRow{}
	boundary, err := q.EventExecutionRecoveryBoundary(ctx, db, mustPgUUID(account))
	if err != nil {
		return nil, err
	}
	var after int64
	afterSub := ""
	for {
		roots, err := q.EventExecutionRecoveryRoots(ctx, db, sqlc.EventExecutionRecoveryRootsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: req.SubscriptionID, EventSource: req.EventSource, EventType: req.EventType, MaxOutbox: boundary, AfterOutbox: after, AfterSubscription: afterSub, PageLimit: api.EventRecoveryItemsPageMax})
		if err != nil {
			return nil, err
		}
		if len(roots) == 0 {
			return out, nil
		}
		encodedRoots := make([]executionRecoveryRoot, 0, len(roots))
		for _, r := range roots {
			encodedRoots = append(encodedRoots, executionRecoveryRoot{r.OutboxID, r.EventSource, r.EventID, r.EventType, r.SubscriptionID, PublishedEventInvocationID(r.InvocationAccountID, r.EventSource, r.EventID, r.SubscriptionID)})
		}
		encoded, err := json.Marshal(encodedRoots)
		if err != nil {
			return nil, err
		}
		rows, err := q.EventExecutionRecoveryCandidates(ctx, db, sqlc.EventExecutionRecoveryCandidatesParams{Roots: encoded, AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Outcome: req.Outcome, FailedBefore: pgtypeFromTime(now.Add(-time.Duration(req.MinAgeSeconds) * time.Second)), NowAt: pgtypeFromTime(now)})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, sqlc.EventRecoveryCandidatesRow(r))
			if len(out) > api.EventRecoveryRecipientsMax {
				return out, nil
			}
		}
		last := roots[len(roots)-1]
		after, afterSub = last.OutboxID, last.SubscriptionID
	}
}

// Preserve captured headers and retry policy. New children receive the parent's
// original execution/result durations; work and start deadlines stay absolute.
func executionRecoveryOptions(inv Invocation, now time.Time) (PlainInvocationReplayOptions, KeyedInvocationReplayOptions) {
	shift := func(at *time.Time) *time.Time {
		if at == nil {
			return nil
		}
		t := now.Add(max(time.Duration(0), at.Sub(inv.CreatedAt)))
		return &t
	}
	plain := PlainInvocationReplayOptions{Headers: inv.Headers, RetryPolicyJSON: inv.RetryPolicyJSON, DeadlineAt: shift(inv.DeadlineAt), ResultRetentionUntil: shift(inv.ResultRetentionUntil)}
	keyed := KeyedInvocationReplayOptions{Headers: plain.Headers, DeadlineAt: plain.DeadlineAt, ResultRetentionUntil: plain.ResultRetentionUntil}
	return plain, keyed
}
func executionRecoveryError(err error) (string, string, error) {
	switch {
	case err == nil:
		return "queued", "", nil
	case errors.Is(err, ErrEventDeliveryCapacity):
		var capacity *EventDeliveryCapacityError
		scope := "unknown"
		if errors.As(err, &capacity) && (capacity.Scope == "account" || capacity.Scope == "app" || capacity.Scope == "consumer") {
			scope = capacity.Scope
		}
		return "pending", scope, nil
	case errors.Is(err, ErrKeyedReplayExpired):
		return "skipped", "expired", nil
	case errors.Is(err, ErrPlatformTenantSuspended):
		return "skipped", "target_unavailable", nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrPlainReplayNotAllowed), errors.Is(err, ErrKeyedReplayNotAllowed):
		return "skipped", "changed", nil
	default:
		return "", "", err
	}
}
func processEventExecutionRecoveryTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventRecoveryJob, item sqlc.EventRecoveryItem, now time.Time) (string, string, error) {
	// Savepoint rolls back admission/capacity side effects on an item-level skip.
	replayTx, err := tx.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = replayTx.Rollback(ctx) }()
	var identity executionRecoveryIdentity
	if err = json.Unmarshal(item.ExpectedProgress, &identity); err != nil {
		return "", "", err
	}
	account, app := uuidString(job.AccountID), uuidString(job.AppID)
	if err = lockInvocationReplayLaneTx(ctx, replayTx, mustPgUUID(identity.InvocationID), job.AccountID, job.AppID); err != nil {
		return "", "", err
	}
	row, err := q.PlainReplayParent(ctx, replayTx, sqlc.PlainReplayParentParams{ID: mustPgUUID(identity.InvocationID), AccountID: job.AccountID})
	if err != nil {
		return executionRecoveryError(mapErr(err))
	}
	parent, err := invocationFromSQL(row)
	if err != nil {
		return "", "", err
	}
	if _, err = q.EventRecoveryApp(ctx, replayTx, sqlc.EventRecoveryAppParams{AccountID: job.AccountID, AppID: job.AppID}); errors.Is(err, pgx.ErrNoRows) {
		return "skipped", "target_unavailable", nil
	} else if err != nil {
		return "", "", err
	}
	if parent.AppID != app || !identity.matches(parent) {
		return "skipped", "changed", nil
	}
	if parent.WorkExpiresAt != nil && !parent.WorkExpiresAt.After(now) || parent.StartDeadlineAt != nil && !parent.StartDeadlineAt.After(now) {
		return "skipped", "expired", nil
	}
	if _, err = q.EventExecutionRecoveryReceipt(ctx, replayTx, sqlc.EventExecutionRecoveryReceiptParams{OutboxID: item.OutboxID, AccountID: job.AccountID, AppID: job.AppID, SubscriptionID: item.SubscriptionID}); errors.Is(err, pgx.ErrNoRows) {
		return "skipped", "receipt_expired", nil
	} else if err != nil {
		return "", "", err
	}
	if _, err = q.PlainReplayIdentity(ctx, replayTx, row.ID); err == nil {
		return "skipped", "changed", nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	if _, err = q.KeyedReplayChildID(ctx, replayTx, row.ID); err == nil {
		return "skipped", "changed", nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	plain, keyed := executionRecoveryOptions(parent, now)
	var replay Invocation
	switch {
	case parent.State == InvocationDeadLetter:
		args, scopeErr := productionDeadLetterScope(account, app, identity.DeadLetterID)
		if scopeErr != nil || identity.DeadLetterID == "" {
			return "skipped", "changed", nil
		}
		dead, readErr := q.LockProductionDeadLetterEvent(ctx, replayTx, sqlc.LockProductionDeadLetterEventParams(args))
		if readErr != nil {
			return executionRecoveryError(mapErr(readErr))
		}
		ev := deadLetterEventFromSQLC(dead)
		if ev.Source != "invocation" || ev.SourceID != parent.ID || ev.ReplayedAt != nil {
			return "skipped", "changed", nil
		}
		_, err = replayDeadLetterEventTx(ctx, replayTx, account, app, ev)
		replay = parent
		replay.ReplayGeneration++
	case parent.WorkPolicyName != "":
		replay, err = replayKeyedInvocationTx(ctx, replayTx, account, parent.ID, keyed)
	default:
		if !plainReplayAllowed(parent) {
			return "skipped", "changed", nil
		}
		replay, err = replayPlainInvocationTx(ctx, replayTx, parent, plain)
	}
	if err != nil {
		return executionRecoveryError(err)
	}
	if err = q.EventRecoveryRecordReplay(ctx, replayTx, sqlc.EventRecoveryRecordReplayParams{JobID: job.ID, Position: item.Position, ReplayInvocationID: mustPgUUID(replay.ID), ReplayGeneration: replay.ReplayGeneration, ReplayCreatedAt: pgtypeFromTime(replay.CreatedAt)}); err != nil {
		return "", "", err
	}
	if err = replayTx.Commit(ctx); err != nil {
		return "", "", err
	}
	return "queued", "", nil
}
