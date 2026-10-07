package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEventStorageCapacity = errors.New("state: event storage capacity exhausted")

type EventStorageCapacityError struct {
	Resource        string
	Limit, Observed int64
}

func (e *EventStorageCapacityError) Error() string {
	return fmt.Sprintf("%s: %s %d exceeds %d", ErrEventStorageCapacity, e.Resource, e.Observed, e.Limit)
}
func (e *EventStorageCapacityError) Unwrap() error { return ErrEventStorageCapacity }

type EventStorageUsageStore interface {
	EventStorageUsage(context.Context, string) (api.EventStorageUsageResponse, error)
}

// TenantPublishedEventStore appends a tenant-authenticated event after
// validating that the tenant is still linked to the target app. Implementors
// keep the link check and durable append in one critical section/transaction.
type TenantPublishedEventStore interface {
	AppendTenantPublishedEvent(context.Context, string, string, string, string, []byte, *string) error
}

func eventStorageExceeded(l api.EventStorageLimits, count, size int64) error {
	if count > l.RetainedEvents {
		return &EventStorageCapacityError{Resource: "events", Limit: l.RetainedEvents, Observed: count}
	}
	if size > l.RetainedBytes {
		return &EventStorageCapacityError{Resource: "bytes", Limit: l.RetainedBytes, Observed: size}
	}
	return nil
}

func customerPublishedEvent(kind string, payload []byte) bool {
	if kind != "event.published" {
		return false
	}
	var identity publishedEventIdentity
	return json.Unmarshal(payload, &identity) == nil && identity.Source != "" && !strings.HasPrefix(identity.Source, "gregale.")
}

// Customer publication serializes on an account mutex. Plan changes hold the
// account row exclusively; a share lock keeps the admission plan current.
// The outbox trigger captures subscriptions in the same transaction as the
// ledger insert. A quota failure rolls both back. Duplicates add neither row.
func (s *PgStore) AppendTenantPublishedEvent(ctx context.Context, actor, accountID, tenantID, appID string, payload []byte, traceID *string) error {
	return s.appendCustomerPublishedEvent(ctx, actor, accountID, tenantID, appID, payload, traceID, nil)
}

func (s *PgStore) appendCustomerPublishedEvent(ctx context.Context, actor string, accountID, tenantID, appID string, payload []byte, traceID *string, at *time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	id := mustPgUUID(accountID)
	plan, err := q.EventStorageAccountPlan(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	limits, ok := api.LimitsFor(api.Plan(plan))
	if !ok {
		return ErrInvalidArgument
	}
	if tenantID != "" && !api.Plan(plan).WorkflowsAllowed() {
		return api.ErrPlanWorkflowsNotAllowed(api.Plan(plan))
	}
	if err = q.EventStorageLockAccount(ctx, tx, id); err != nil {
		return err
	}
	var identity publishedEventIdentity
	if err = json.Unmarshal(payload, &identity); err != nil {
		return err
	}
	if tenantID != "" {
		if identity.AppID == "" || identity.PlatformTenantID == "" || identity.TenantEventID == "" ||
			canonicalMemUUID(identity.AppID) != canonicalMemUUID(appID) || canonicalMemUUID(identity.PlatformTenantID) != canonicalMemUUID(tenantID) {
			return ErrNotFound
		}
		if err = lockTenantPublishedEventBinding(ctx, tx, accountID, tenantID, appID); err != nil {
			return err
		}
	}
	existing, err := q.EventStorageIdentity(ctx, tx, sqlc.EventStorageIdentityParams{AccountID: id, Source: identity.Source, EventID: identity.ID, EventType: identity.Type, SchemaVersion: identity.SchemaVersion, EventData: identity.Data})
	if err == nil {
		if !existing {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	usage, err := q.EventStorageUsage(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = eventStorageExceeded(limits.EventStorage, usage.RetainedEvents+1, usage.RetainedBytes); err != nil {
		return err
	}
	if err = q.EventStorageAppend(ctx, tx, sqlc.EventStorageAppendParams{Actor: actor, AccountID: id, Payload: payload, TraceID: eventStorageTrace(traceID), OccurredAt: nullableTimestamptzPtr(at)}); err != nil {
		return err
	}
	charge, err := q.EventStorageAcceptedCharge(ctx, tx, sqlc.EventStorageAcceptedChargeParams{AccountID: id, Source: identity.Source, EventID: identity.ID})
	if err != nil {
		return err
	}
	if err = eventStorageExceeded(limits.EventStorage, usage.RetainedEvents+1, usage.RetainedBytes+charge); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockTenantPublishedEventBinding serializes event acceptance with tenant
// suspension, app maintenance/deletion, and link revocation. The held row
// locks live through the events insert and its fanout trigger.
func lockTenantPublishedEventBinding(ctx context.Context, tx pgx.Tx, accountID, tenantID, appID string) error {
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM platform_tenants
		WHERE id=$1::uuid AND account_id=$2::uuid AND status='active' FOR SHARE`, tenantID, accountID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM apps
		WHERE id=$1::uuid AND account_id=$2::uuid AND status<>'deleted'
		  AND platform_tenant_required AND NOT maintenance_mode FOR SHARE`, appID, accountID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	err := tx.QueryRow(ctx, `SELECT id FROM api_consumers
		WHERE account_id=$1::uuid AND app_id=$2::uuid AND platform_tenant_id=$3::uuid
		  AND status='active' AND revoked_at IS NULL FOR SHARE`, accountID, appID, tenantID).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM tenant_surfaces
		WHERE account_id=$1::uuid AND app_id=$2::uuid AND platform_tenant_id=$3::uuid
		  AND status='active' FOR SHARE`, accountID, appID, tenantID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *PgStore) EventStorageUsage(ctx context.Context, accountID string) (api.EventStorageUsageResponse, error) {
	row, err := sqlc.New().EventStoragePublicUsage(ctx, s.pool, mustPgUUID(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventStorageUsageResponse{}, ErrNotFound
	}
	if err != nil {
		return api.EventStorageUsageResponse{}, err
	}
	l, ok := api.LimitsFor(api.Plan(row.Plan))
	if !ok {
		return api.EventStorageUsageResponse{}, ErrInvalidArgument
	}
	return api.EventStorageUsageResponse{RetainedEvents: row.RetainedEvents, RetainedBytes: row.RetainedBytes, PendingEvents: row.PendingEvents, OldestPendingAt: eventStorageTime(row.OldestPendingAt), Limits: l.EventStorage}, nil
}

func (m *MemStore) EventStorageUsage(_ context.Context, accountID string) (api.EventStorageUsageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventStorageUsageLocked(accountID)
}

func (m *MemStore) AppendTenantPublishedEvent(_ context.Context, actor, accountID, tenantID, appID string, payload []byte, traceID *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var identity publishedEventIdentity
	if json.Unmarshal(payload, &identity) != nil || identity.AppID == "" || identity.PlatformTenantID == "" || identity.TenantEventID == "" ||
		!sameMemUUID(identity.AppID, appID) || !sameMemUUID(identity.PlatformTenantID, tenantID) ||
		!m.workflowOutboundTenantLinkActiveLocked(accountID, tenantID, appID) {
		return ErrNotFound
	}
	app, ok := m.apps[appID]
	if !ok || !app.PlatformTenantRequired || app.MaintenanceMode || app.Status == AppDeleted || !sameMemUUID(app.AccountID, accountID) {
		return ErrNotFound
	}
	account, ok := m.accounts[accountID]
	if !ok {
		for _, candidate := range m.accounts {
			if sameMemUUID(candidate.ID, accountID) {
				account, ok = candidate, true
				break
			}
		}
	}
	if !ok || !account.Active() {
		return ErrNotFound
	}
	if !account.Plan.WorkflowsAllowed() {
		return api.ErrPlanWorkflowsNotAllowed(account.Plan)
	}
	subject := accountID
	return m.appendEventLocked(actor, "event.published", &subject, payload, traceID, time.Now().UTC())
}

func (m *MemStore) eventStorageUsageLocked(accountID string) (api.EventStorageUsageResponse, error) {
	var plan api.Plan
	var found bool
	for _, account := range m.accounts {
		if sameMemUUID(account.ID, accountID) {
			plan, found = account.Plan, true
			break
		}
	}
	if !found {
		return api.EventStorageUsageResponse{}, ErrNotFound
	}
	l, ok := api.LimitsFor(plan)
	if !ok {
		return api.EventStorageUsageResponse{}, ErrInvalidArgument
	}
	usage := api.EventStorageUsageResponse{Limits: l.EventStorage}
	prefix := canonicalMemUUID(accountID) + "\x00"
	for key, work := range m.eventFanout {
		if !strings.HasPrefix(key, prefix) || work.StorageBytes == 0 {
			continue
		}
		usage.RetainedEvents++
		usage.RetainedBytes += work.StorageBytes
		if !work.Delivered {
			usage.PendingEvents++
			if usage.OldestPendingAt == nil || work.CreatedAt.Before(*usage.OldestPendingAt) {
				at := work.CreatedAt
				usage.OldestPendingAt = &at
			}
		}
	}
	return usage, nil
}

// MemStore estimates the same serialized JSON values charged by PostgreSQL.
// Account limits concern logical JSON bytes, not TOAST compression or indexes.
func eventJSONStorageBytes(raw []byte) int64 {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return 0
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(value) != nil {
		return 0
	}
	data := bytes.TrimSpace(b.Bytes())
	size := int64(len(data))
	quoted, escaped := false, false
	for _, c := range data {
		if escaped {
			escaped = false
			continue
		}
		if quoted && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && (c == ',' || c == ':') {
			size++
		}
	}
	return size
}

func eventStorageTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}
func eventStorageTrace(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}
