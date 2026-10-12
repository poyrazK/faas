package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEventRecoveryChanged = errors.New("event recovery expectation changed")

type EventRoutingRecoveryStore interface {
	ReplayFailedWorkflowEventRecipient(context.Context, string, string, string, string, string, api.EventRoutingRecoveryExpectation) error
}

func (s *PgStore) ReplayFailedWorkflowEventRecipient(ctx context.Context, account, app, source, event, subscription string, e api.EventRoutingRecoveryExpectation) error {
	if !e.Valid() {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin guarded event recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replayWorkflowRecoveryTx(ctx, sqlc.New(), tx, account, app, source, event, subscription, e, time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit guarded event recovery: %w", err)
	}
	return nil
}

func replayWorkflowRecoveryTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, source, event, subscription string, e api.EventRoutingRecoveryExpectation, now time.Time) error {
	// Same lock order as automation publication; keep revision stable until commit.
	if err := q.LockWorkflowRunAdmission(ctx, tx, app); err != nil {
		return fmt.Errorf("lock recovery automation: %w", err)
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(app))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock recovery app: %w", err)
	}
	if pgUUIDString(target.AccountID) != account || target.AppStatus == string(AppDeleted) {
		return ErrNotFound
	}
	version, err := q.EventRecoveryPublishedVersion(ctx, tx, sqlc.EventRecoveryPublishedVersionParams{AppID: mustPgUUID(app), Name: e.WorkflowName})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && version != e.PublishedVersion {
		return ErrEventRecoveryChanged
	}
	if err != nil {
		return fmt.Errorf("read recovery revision: %w", err)
	}
	row, err := q.EventRecipientReplayReceipt(ctx, tx, sqlc.EventRecipientReplayReceiptParams{AccountID: mustPgUUID(account), AppID: app, EventSource: source, EventID: event, SubscriptionID: subscription})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock recovery receipt: %w", err)
	}
	captured, err := q.EventAgeReplayTarget(ctx, tx, sqlc.EventAgeReplayTargetParams{OutboxID: row.ID, SubscriptionID: subscription})
	if err != nil {
		return fmt.Errorf("read recovery snapshot: %w", err)
	}
	var recipient PublishedEventRecipient
	if err := json.Unmarshal(captured.Recipient, &recipient); err != nil {
		return fmt.Errorf("decode recovery recipient: %w", err)
	}
	if !eventRecoveryWorkflowMatches(recipient, e.WorkflowName) {
		return ErrEventRecoveryChanged
	}
	count, err := q.EventRecoveryReplayCount(ctx, tx, sqlc.EventRecoveryReplayCountParams{OutboxID: row.ID, SubscriptionID: subscription})
	if err != nil {
		return fmt.Errorf("read recovery replay count: %w", err)
	}
	if count != e.ReplayCount {
		return ErrEventRecoveryChanged
	}
	var prior PublishedEventRecipientProgress
	if json.Unmarshal(row.Progress, &prior) != nil || prior.State != PublishedEventRecipientFailed {
		return ErrEventRecoveryChanged
	}
	if row.RecipientClaims {
		routing, err := q.EventRecoveryRoutingGeneration(ctx, tx, sqlc.EventRecoveryRoutingGenerationParams{OutboxID: row.ID, SubscriptionID: subscription})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEventRecoveryChanged
		}
		if err != nil {
			return fmt.Errorf("lock recovery generation: %w", err)
		}
		if e.Generation == nil || routing.Generation != *e.Generation || routing.State != "failed" {
			return ErrEventRecoveryChanged
		}
	} else {
		settled, err := q.EventRecoveryLegacySettled(ctx, tx, row.ID)
		if err != nil {
			return fmt.Errorf("read recovery settlement: %w", err)
		}
		if e.Generation != nil || !settled {
			return ErrEventRecoveryChanged
		}
	}
	if err := replayEventRecipientTx(ctx, q, tx, row.ID, app, subscription, row.Progress, row.RecipientClaims, now); err != nil {
		return fmt.Errorf("queue guarded event recovery: %w", err)
	}
	return nil
}

func eventRecoveryWorkflowMatches(r PublishedEventRecipient, name string) bool {
	var workflow struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(r.Workflow, &workflow) == nil && workflow.Name == name
}

func (m *MemStore) ReplayFailedWorkflowEventRecipient(_ context.Context, account, app, source, event, subscription string, e api.EventRoutingRecoveryExpectation) error {
	if !e.Valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned, exists := m.eventSubscriptionAppLocked(app)
	if !exists || !sameMemUUID(owned.AccountID, account) || owned.Status == AppDeleted {
		return ErrNotFound
	}
	found := false
	for _, a := range m.automationRecordsLocked(owned.ID) {
		if a.Name == e.WorkflowName && a.PublishedVersion == e.PublishedVersion {
			found = true
		}
	}
	if !found {
		return ErrEventRecoveryChanged
	}
	return m.replayFailedEventRecipientLocked(account, app, source, event, subscription, false, &e)
}
func (m *MemStore) checkEventRecoveryLocked(work *PublishedEventWork, r PublishedEventRecipient, e api.EventRoutingRecoveryExpectation) error {
	if !eventRecoveryWorkflowMatches(r, e.WorkflowName) {
		return ErrEventRecoveryChanged
	}
	count := int64(0)
	for _, a := range m.eventFanoutAttempts {
		if a.OutboxID == work.ID && a.SubscriptionID == r.ID && a.Action == EventFanoutAttemptActionReplay {
			count++
		}
	}
	if count != e.ReplayCount {
		return ErrEventRecoveryChanged
	}
	if work.RecipientClaims {
		own := work.routingRecipients[r.ID]
		if own == nil || e.Generation == nil || own.Generation != *e.Generation || own.State != PublishedEventRecipientFailed {
			return ErrEventRecoveryChanged
		}
	} else if e.Generation != nil {
		return ErrEventRecoveryChanged
	}
	progress := work.RecipientProgress[r.ID]
	if progress.State != PublishedEventRecipientFailed || !work.Delivered && !work.RecipientClaims {
		return ErrEventRecoveryChanged
	}
	return nil
}
