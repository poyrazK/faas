package state

// adr: 20261101030512 — a production manifest deploy's event subscriptions
// take effect when the deployment serves all production traffic, under the
// same rule as staged crons.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type deploymentEventSubscriptionsKey struct{}

// ContextWithDeploymentEventSubscriptions carries the subscriptions a
// production manifest deploy declares into deployment creation, which records
// them in the same transaction. A nil slice records nothing; an empty one
// declares none, so every subscription of the app is disabled once the
// deployment is live.
func ContextWithDeploymentEventSubscriptions(ctx context.Context, declared []string) context.Context {
	if declared == nil {
		return ctx
	}
	ids := append([]string{}, declared...)
	return context.WithValue(ctx, deploymentEventSubscriptionsKey{}, &ids)
}

func deploymentEventSubscriptionIntent(ctx context.Context) *[]string {
	ids, _ := ctx.Value(deploymentEventSubscriptionsKey{}).(*[]string)
	return ids
}

// EventSubscriptionActivationChange reports the subscriptions one
// deployment's activation turned on or off.
type EventSubscriptionActivationChange struct {
	AccountID, AppID, DeploymentID string
	Enabled, Disabled              []string
}

// EventSubscriptionActivationStore applies recorded subscription intents once
// their deployment serves all production traffic, and discards those of
// deployments that never will.
type EventSubscriptionActivationStore interface {
	ReconcileEventSubscriptionActivations(context.Context) ([]EventSubscriptionActivationChange, error)
}

type memEventSubscriptionIntent struct {
	AppID    string
	Declared []string
	Status   string
	Reason   string
	Updated  time.Time
}

func recordDeploymentEventSubscriptionIntentTx(ctx context.Context, tx pgx.Tx, d Deployment, declared []string) error {
	ids := make([]pgtype.UUID, 0, len(declared))
	for _, id := range declared {
		ids = append(ids, mustPgUUID(id))
	}
	if err := sqlc.New().InsertDeploymentEventSubscriptionIntent(ctx, tx, sqlc.InsertDeploymentEventSubscriptionIntentParams{
		DeploymentID: mustPgUUID(d.ID), AppID: mustPgUUID(d.AppID), DeclaredSubscriptionIds: ids,
	}); err != nil {
		return fmt.Errorf("state: record deployment event subscription intent: %w", err)
	}
	return nil
}

func (m *MemStore) recordDeploymentEventSubscriptionIntentLocked(d Deployment, declared []string) {
	if m.eventSubscriptionIntents == nil {
		m.eventSubscriptionIntents = map[string]memEventSubscriptionIntent{}
	}
	if _, exists := m.eventSubscriptionIntents[d.ID]; exists {
		return
	}
	m.eventSubscriptionIntents[d.ID] = memEventSubscriptionIntent{AppID: d.AppID, Declared: append([]string{}, declared...), Status: "prepared", Updated: time.Now().UTC()}
}

// ReconcileEventSubscriptionActivations implements
// EventSubscriptionActivationStore.
func (s *PgStore) ReconcileEventSubscriptionActivations(ctx context.Context) ([]EventSubscriptionActivationChange, error) {
	ids, err := sqlc.New().ListPreparedEventSubscriptionIntents(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list event subscription intents: %w", err)
	}
	changes := []EventSubscriptionActivationChange{}
	for _, id := range ids {
		change, err := s.reconcileEventSubscriptionIntent(ctx, id)
		if err != nil {
			return changes, err
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}
	return changes, nil
}

func (s *PgStore) reconcileEventSubscriptionIntent(ctx context.Context, deploymentID string) (*EventSubscriptionActivationChange, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	d, err := q.LockEventSubscriptionIntentDeployment(ctx, tx, mustPgUUID(deploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: lock event subscription intent: %w", err)
	}
	status := cronDeploymentDisposition(d.Status, d.Scope, d.RolloutState, int(d.TrafficPercent), int(d.CanaryTotalSteps), d.TrafficPercentExplicit, d.CompetingTraffic, d.DeploymentID, d.LatestID)
	if status == "prepared" {
		return nil, nil
	}
	reason := cronDispositionReason(d.Status, d.Scope, d.RolloutState, int(d.TrafficPercent), int(d.CanaryTotalSteps), d.TrafficPercentExplicit, d.CompetingTraffic, d.DeploymentID, d.LatestID)
	var change *EventSubscriptionActivationChange
	if status == "activated" {
		reason = ""
		declared := make([]pgtype.UUID, 0, len(d.Declared))
		for _, id := range d.Declared {
			declared = append(declared, mustPgUUID(id))
		}
		rows, err := q.ApplyEventSubscriptionIntent(ctx, tx, sqlc.ApplyEventSubscriptionIntentParams{Declared: declared, AppID: mustPgUUID(d.AppID)})
		if err != nil {
			return nil, fmt.Errorf("state: apply event subscription intent: %w", err)
		}
		var accountID string
		if err := tx.QueryRow(ctx, `select account_id::text from apps where id = $1`, mustPgUUID(d.AppID)).Scan(&accountID); err != nil {
			return nil, fmt.Errorf("state: read app account for event subscription intent: %w", mapErr(err))
		}
		change = &EventSubscriptionActivationChange{AccountID: accountID, AppID: d.AppID, DeploymentID: d.DeploymentID}
		for _, row := range rows {
			if row.Enabled {
				change.Enabled = append(change.Enabled, row.ID)
			} else {
				change.Disabled = append(change.Disabled, row.ID)
			}
		}
	}
	if err := q.SettleEventSubscriptionIntent(ctx, tx, sqlc.SettleEventSubscriptionIntentParams{Status: status, Reason: truncateIntentReason(reason), DeploymentID: mustPgUUID(deploymentID)}); err != nil {
		return nil, fmt.Errorf("state: settle event subscription intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return change, nil
}

func truncateIntentReason(reason string) string {
	if len(reason) > 64 {
		return reason[:64]
	}
	return reason
}

// ReconcileEventSubscriptionActivations implements
// EventSubscriptionActivationStore.
func (m *MemStore) ReconcileEventSubscriptionActivations(_ context.Context) ([]EventSubscriptionActivationChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.eventSubscriptionIntents))
	for id, intent := range m.eventSubscriptionIntents {
		if intent.Status == "prepared" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	changes := []EventSubscriptionActivationChange{}
	for _, id := range ids {
		intent := m.eventSubscriptionIntents[id]
		d, ok := m.deployments[id]
		if !ok {
			intent.Status, intent.Reason = "discarded", "deployment_missing"
			m.eventSubscriptionIntents[id] = intent
			continue
		}
		latest, competing := m.policyDeploymentDispositionLocked(d)
		status := cronDeploymentDisposition(string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
		if status == "prepared" {
			continue
		}
		intent.Status = status
		intent.Reason = cronDispositionReason(string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
		if status == "activated" {
			intent.Reason = ""
			changes = append(changes, m.applyEventSubscriptionIntentLocked(intent, d.ID))
		}
		intent.Updated = time.Now().UTC()
		m.eventSubscriptionIntents[id] = intent
	}
	return changes, nil
}

// policyDeploymentDispositionLocked mirrors ReadCronActivationDeployment's
// latest and competing-traffic inputs for one deployment.
func (m *MemStore) policyDeploymentDispositionLocked(d Deployment) (string, bool) {
	latest := ""
	var newest Deployment
	competing := false
	for _, candidate := range m.deployments {
		if candidate.AppID != d.AppID || normalizedDeploymentScope(candidate.Scope) != DefaultEnvScope {
			continue
		}
		if candidate.ID != d.ID && candidate.Status == DeployLive && candidate.TrafficPercent > 0 {
			competing = true
		}
		if candidate.TrafficPercentExplicit && candidate.TrafficPercent == 0 && candidate.CanaryTotalSteps == 0 {
			continue
		}
		if latest == "" || candidate.CreatedAt.After(newest.CreatedAt) || candidate.CreatedAt.Equal(newest.CreatedAt) && candidate.ID > newest.ID {
			newest = candidate
			latest = candidate.ID
		}
	}
	return latest, competing
}

func (m *MemStore) applyEventSubscriptionIntentLocked(intent memEventSubscriptionIntent, deploymentID string) EventSubscriptionActivationChange {
	declared := make(map[string]bool, len(intent.Declared))
	for _, id := range intent.Declared {
		declared[canonicalMemUUID(id)] = true
	}
	change := EventSubscriptionActivationChange{AppID: intent.AppID, DeploymentID: deploymentID}
	if app, ok := m.eventSubscriptionAppLocked(intent.AppID); ok {
		change.AccountID = app.AccountID
	}
	now := time.Now().UTC()
	for key, subscription := range m.eventSubscriptions {
		if !sameMemUUID(subscription.AppID, intent.AppID) {
			continue
		}
		want := declared[canonicalMemUUID(subscription.ID)]
		if subscription.Enabled == want {
			continue
		}
		subscription.Enabled, subscription.UpdatedAt = want, now
		m.eventSubscriptions[key] = subscription
		if want {
			change.Enabled = append(change.Enabled, subscription.ID)
		} else {
			change.Disabled = append(change.Disabled, subscription.ID)
		}
	}
	sort.Strings(change.Enabled)
	sort.Strings(change.Disabled)
	return change
}
