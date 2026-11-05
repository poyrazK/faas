package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// Customers acting on their own account (ADR-20261021183000): a consumer
// key with the billing scope switches plans or closes the account, and the
// app owner hears about it, and about sign-ups, through app webhooks that
// are enqueued with the change itself.

// APIConsumerSelfServiceStore makes a customer's own plan changes and
// closing durable together with their webhook events. Optional, like the
// other pricing stores.
type APIConsumerSelfServiceStore interface {
	// ChangeAPIConsumerPlanSelf appends a's plan assignment and enqueues
	// consumer.plan_changed from fromPlanID. ErrConflict when the consumer
	// is revoked or already has an assignment at a.EffectiveFrom.
	ChangeAPIConsumerPlanSelf(ctx context.Context, a APIConsumerPlanAssignment, fromPlanID string) (APIConsumerPlanAssignment, error)
	// CloseAPIConsumerSelf revokes the consumer and enqueues
	// consumer.closed; a revoked consumer returns changed=false.
	CloseAPIConsumerSelf(ctx context.Context, accountID, consumerID string) (APIConsumer, bool, error)
}

var (
	_ APIConsumerSelfServiceStore = (*MemStore)(nil)
	_ APIConsumerSelfServiceStore = (*PgStore)(nil)
)

func consumerSignedUpWebhookPayload(s APIConsumerSignup, c APIConsumer, in APIConsumerSignupCompletion) (json.RawMessage, error) {
	if s.CompletedAt == nil {
		return nil, errors.New("state: completed sign-up has no completed_at")
	}
	payload := api.APIConsumerSignedUpWebhookPayload{SignupID: s.ID, AppID: s.AppID, ConsumerID: c.ID, ExternalRef: c.ExternalRef,
		Name: s.Name, Email: s.Email, PlanID: s.PlanID, Livemode: s.Livemode, SignedUpAt: s.CompletedAt.UTC()}
	if in.TrialDays > 0 {
		end := in.EffectiveFrom.UTC().AddDate(0, 0, in.TrialDays)
		payload.TrialEndsAt = &end
	}
	return json.Marshal(payload)
}

// enqueueAppWebhookEventLocked mirrors enqueueAppWebhookEventTx: one
// outbox event per event and source, for the app's enabled hooks that
// take it. The caller holds m.mu.
func (m *MemStore) enqueueAppWebhookEventLocked(accountID, appID string, event AppWebhookEvent, sourceID string, payload json.RawMessage, now time.Time) {
	var recipients []string
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == appID && hook.AccountID == accountID && hook.Enabled &&
			(len(hook.EventFilter) == 0 || slices.Contains(hook.EventFilter, string(event))) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) == 0 {
		return
	}
	for _, existing := range m.appWebhookEventOutbox {
		if existing.Event == event && existing.SourceID == sourceID {
			return
		}
	}
	sort.Strings(recipients)
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = make(map[string]appWebhookOutboxEvent)
	}
	id := uuid.NewString()
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: accountID, AppID: appID, Event: event, SourceID: sourceID,
		Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: now}
}

// enqueueAppWebhookEventTx records an app webhook event in tx for the
// app's enabled hooks that take it, once per event and source; the relay
// delivers it after commit.
func enqueueAppWebhookEventTx(ctx context.Context, tx pgx.Tx, accountID, appID string, event AppWebhookEvent, sourceID string, payload json.RawMessage) error {
	if _, err := tx.Exec(ctx, `
		insert into app_webhook_event_outbox
			(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
		select $1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, array_agg(h.id order by h.id)
		  from app_webhooks h
		 where h.account_id = $1::uuid and h.app_id = $2::uuid
		   and h.scope = 'app' and h.enabled
		   and (cardinality(h.event_filter) = 0 or $3 = any(h.event_filter))
		having count(*) > 0
		on conflict (event, source_id) do nothing`, accountID, appID, string(event), sourceID, string(payload)); err != nil {
		return fmt.Errorf("state: enqueue %s webhook event: %w", event, err)
	}
	return nil
}

func planChangedWebhookPayload(a APIConsumerPlanAssignment, c APIConsumer, fromPlanID string) (json.RawMessage, error) {
	return json.Marshal(api.APIConsumerPlanChangedWebhookPayload{AssignmentID: a.ID, AppID: a.AppID, ConsumerID: a.ConsumerID,
		ExternalRef: c.ExternalRef, FromPlanID: fromPlanID, ToPlanID: a.PlanID, EffectiveFrom: a.EffectiveFrom.UTC(), ChangedAt: a.CreatedAt.UTC()})
}

func consumerClosedWebhookPayload(c APIConsumer) (json.RawMessage, error) {
	if c.RevokedAt == nil {
		return nil, errors.New("state: closed consumer has no revoked_at")
	}
	return json.Marshal(api.APIConsumerClosedWebhookPayload{AppID: c.AppID, ConsumerID: c.ID, ExternalRef: c.ExternalRef, ClosedAt: c.RevokedAt.UTC()})
}

func (m *MemStore) ChangeAPIConsumerPlanSelf(_ context.Context, a APIConsumerPlanAssignment, fromPlanID string) (APIConsumerPlanAssignment, error) {
	if err := validateAPIConsumerPlanAssignment(a); err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[a.ConsumerID]
	if !ok || c.AccountID != a.AccountID || c.AppID != a.AppID {
		return APIConsumerPlanAssignment{}, ErrNotFound
	}
	if !c.Active() {
		return APIConsumerPlanAssignment{}, ErrConflict
	}
	if a.PlanID != "" {
		if plan, ok := m.apiConsumerPlans[a.PlanID]; !ok || plan.AccountID != a.AccountID || plan.AppID != a.AppID {
			return APIConsumerPlanAssignment{}, ErrNotFound
		}
	}
	for _, existing := range m.apiConsumerPlanAssignments {
		if existing.ConsumerID == a.ConsumerID && existing.EffectiveFrom.Equal(a.EffectiveFrom) {
			return APIConsumerPlanAssignment{}, ErrConflict
		}
	}
	a.ID, a.CreatedAt, a.EffectiveFrom = uuid.NewString(), time.Now().UTC(), a.EffectiveFrom.UTC()
	payload, err := planChangedWebhookPayload(a, c, fromPlanID)
	if err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	m.apiConsumerPlanAssignments[a.ID] = a
	m.enqueueAppWebhookEventLocked(a.AccountID, a.AppID, AppWebhookEventConsumerPlanChanged, a.ID, payload, a.CreatedAt)
	return a, nil
}

func (m *MemStore) CloseAPIConsumerSelf(_ context.Context, accountID, consumerID string) (APIConsumer, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return APIConsumer{}, false, ErrNotFound
	}
	if !c.Active() {
		return c, false, nil
	}
	now := time.Now().UTC()
	c.Status, c.RevokedAt, c.UpdatedAt = APIConsumerStatusRevoked, &now, now
	payload, err := consumerClosedWebhookPayload(c)
	if err != nil {
		return APIConsumer{}, false, err
	}
	m.apiConsumers[c.ID] = c
	m.enqueueAppWebhookEventLocked(c.AccountID, c.AppID, AppWebhookEventConsumerClosed, c.ID, payload, now)
	return c, true, nil
}

func (s *PgStore) ChangeAPIConsumerPlanSelf(ctx context.Context, a APIConsumerPlanAssignment, fromPlanID string) (APIConsumerPlanAssignment, error) {
	if err := validateAPIConsumerPlanAssignment(a); err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerPlanAssignment{}, fmt.Errorf("begin consumer plan change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid for update`, a.ConsumerID, a.AccountID, a.AppID))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerPlanAssignment{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerPlanAssignment{}, fmt.Errorf("lock consumer for plan change: %w", err)
	}
	if !c.Active() {
		return APIConsumerPlanAssignment{}, ErrConflict
	}
	out, err := scanAPIConsumerPlanAssignment(tx.QueryRow(ctx, `insert into api_consumer_plan_assignments
		(account_id, app_id, consumer_id, plan_id, effective_from)
		values ($1::uuid, $2::uuid, $3::uuid, nullif($4::text, '')::uuid, $5)
		returning `+apiConsumerPlanAssignmentCols, a.AccountID, a.AppID, a.ConsumerID, a.PlanID, a.EffectiveFrom.UTC()))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return APIConsumerPlanAssignment{}, ErrConflict
		case "23503":
			return APIConsumerPlanAssignment{}, ErrNotFound
		}
	}
	if err != nil {
		return APIConsumerPlanAssignment{}, fmt.Errorf("assign consumer plan: %w", err)
	}
	payload, err := planChangedWebhookPayload(out, c, fromPlanID)
	if err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	if err := enqueueAppWebhookEventTx(ctx, tx, out.AccountID, out.AppID, AppWebhookEventConsumerPlanChanged, out.ID, payload); err != nil {
		return APIConsumerPlanAssignment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerPlanAssignment{}, fmt.Errorf("commit consumer plan change: %w", err)
	}
	return out, nil
}

func (s *PgStore) CloseAPIConsumerSelf(ctx context.Context, accountID, consumerID string) (APIConsumer, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumer{}, false, fmt.Errorf("begin consumer close: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := scanAPIConsumerRow(tx.QueryRow(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where id = $1::uuid and account_id = $2::uuid for update`, consumerID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumer{}, false, ErrNotFound
	}
	if err != nil {
		return APIConsumer{}, false, fmt.Errorf("lock consumer for close: %w", err)
	}
	if !c.Active() {
		return c, false, nil
	}
	c, err = scanAPIConsumerRow(tx.QueryRow(ctx, `update api_consumers
		    set status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now()
		  where id = $1::uuid returning `+apiConsumerSelectCols, consumerID))
	if err != nil {
		return APIConsumer{}, false, fmt.Errorf("close consumer: %w", err)
	}
	payload, err := consumerClosedWebhookPayload(c)
	if err != nil {
		return APIConsumer{}, false, err
	}
	if err := enqueueAppWebhookEventTx(ctx, tx, c.AccountID, c.AppID, AppWebhookEventConsumerClosed, c.ID, payload); err != nil {
		return APIConsumer{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumer{}, false, fmt.Errorf("commit consumer close: %w", err)
	}
	return c, true, nil
}
