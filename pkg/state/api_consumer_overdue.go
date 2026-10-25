package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// APIConsumerBillingPolicy is an app's rule for unpaid invoices (ADR-1315):
// a consumer with an exported invoice still unpaid BlockOverdueAfterDays
// after its due date is refused at the gateway until it is paid or voided.
type APIConsumerBillingPolicy struct {
	AccountID             string
	AppID                 string
	BlockOverdueAfterDays int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// APIConsumerOverdueHold blocks one consumer because of its oldest overdue
// invoice. Since is when the hold was placed.
type APIConsumerOverdueHold struct {
	AccountID  string
	AppID      string
	ConsumerID string
	ExportID   string
	DueAt      time.Time
	Since      time.Time
}

// APIConsumerOverdueStore persists overdue policies and holds (ADR-1315).
type APIConsumerOverdueStore interface {
	PutAPIConsumerBillingPolicy(ctx context.Context, p APIConsumerBillingPolicy) (APIConsumerBillingPolicy, error)
	// GetAPIConsumerBillingPolicy is ErrNotFound when the app has none.
	GetAPIConsumerBillingPolicy(ctx context.Context, accountID, appID string) (APIConsumerBillingPolicy, error)
	// DeleteAPIConsumerBillingPolicy removes the policy; the next sync
	// lifts its holds.
	DeleteAPIConsumerBillingPolicy(ctx context.Context, accountID, appID string) error
	// SyncAPIConsumerOverdueHolds places a hold on every consumer whose
	// oldest qualifying invoice is overdue at now and lifts every other
	// hold. It is idempotent; placed and lifted report what changed. A held
	// invoice's next payment sync is brought within
	// api.ConsumerOverdueHeldSyncInterval.
	SyncAPIConsumerOverdueHolds(ctx context.Context, now time.Time) (placed, lifted []APIConsumerOverdueHold, err error)
	ListAPIConsumerOverdueHolds(ctx context.Context, accountID, appID string) ([]APIConsumerOverdueHold, error)
}

var (
	_ APIConsumerOverdueStore = (*MemStore)(nil)
	_ APIConsumerOverdueStore = (*PgStore)(nil)
)

func validateAPIConsumerBillingPolicy(p APIConsumerBillingPolicy) error {
	if _, err := uuid.Parse(p.AccountID); err != nil {
		return fmt.Errorf("%w: account_id must be a UUID", ErrInvalidArgument)
	}
	if _, err := uuid.Parse(p.AppID); err != nil {
		return fmt.Errorf("%w: app_id must be a UUID", ErrInvalidArgument)
	}
	if p.BlockOverdueAfterDays < 0 || p.BlockOverdueAfterDays > api.ConsumerOverdueBlockMaxDays {
		return fmt.Errorf("%w: block_overdue_after_days must be 0 to %d", ErrInvalidArgument, api.ConsumerOverdueBlockMaxDays)
	}
	return nil
}

// overdueExport reports whether an exported invoice blocks its consumer at
// now under a policy: completed, still open or uncollectible with an amount
// left, due more than days ago, and that status read within
// api.ConsumerOverdueStatusMaxAge (ADR-20261019180000).
func overdueExport(e StatementInvoiceExport, days int, now time.Time) bool {
	return e.Status == StatementInvoiceExportCompleted && e.DueAt != nil &&
		(e.PaymentStatus == InvoicePaymentOpen || e.PaymentStatus == InvoicePaymentUncollectible) &&
		(e.AmountRemainingMinor == nil || *e.AmountRemainingMinor > 0) &&
		!e.DueAt.AddDate(0, 0, days).After(now) &&
		e.StatusSyncedAt != nil && !e.StatusSyncedAt.Before(now.Add(-api.ConsumerOverdueStatusMaxAge))
}

// --- MemStore ---

func (m *MemStore) PutAPIConsumerBillingPolicy(_ context.Context, p APIConsumerBillingPolicy) (APIConsumerBillingPolicy, error) {
	if err := validateAPIConsumerBillingPolicy(p); err != nil {
		return APIConsumerBillingPolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiConsumerBillingPolicies == nil {
		m.apiConsumerBillingPolicies = map[string]APIConsumerBillingPolicy{}
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	if prior, ok := m.apiConsumerBillingPolicies[p.AppID]; ok {
		p.CreatedAt = prior.CreatedAt
	}
	m.apiConsumerBillingPolicies[p.AppID] = p
	return p, nil
}

func (m *MemStore) GetAPIConsumerBillingPolicy(_ context.Context, accountID, appID string) (APIConsumerBillingPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiConsumerBillingPolicies[appID]
	if !ok || p.AccountID != accountID {
		return APIConsumerBillingPolicy{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) DeleteAPIConsumerBillingPolicy(_ context.Context, accountID, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.apiConsumerBillingPolicies[appID]; !ok || p.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.apiConsumerBillingPolicies, appID)
	return nil
}

func (m *MemStore) SyncAPIConsumerOverdueHolds(_ context.Context, now time.Time) ([]APIConsumerOverdueHold, []APIConsumerOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	due := map[string]APIConsumerOverdueHold{}
	for _, e := range m.statementInvoiceExports {
		if e.SourceKind != StatementSourceAppConsumer {
			continue
		}
		statement, ok := m.apiConsumerUsageStatements[e.StatementID]
		if !ok {
			continue
		}
		policy, ok := m.apiConsumerBillingPolicies[statement.AppID]
		if !ok || policy.AccountID != statement.AccountID || !overdueExport(e, policy.BlockOverdueAfterDays, now) {
			continue
		}
		if prior, ok := due[statement.ConsumerID]; ok && !e.DueAt.Before(prior.DueAt) {
			continue
		}
		due[statement.ConsumerID] = APIConsumerOverdueHold{AccountID: statement.AccountID, AppID: statement.AppID,
			ConsumerID: statement.ConsumerID, ExportID: e.ID, DueAt: e.DueAt.UTC(), Since: now.UTC()}
	}
	if m.apiConsumerOverdueHolds == nil {
		m.apiConsumerOverdueHolds = map[string]APIConsumerOverdueHold{}
	}
	var placed, lifted []APIConsumerOverdueHold
	for consumerID, hold := range m.apiConsumerOverdueHolds {
		if _, still := due[consumerID]; !still {
			lifted = append(lifted, hold)
			delete(m.apiConsumerOverdueHolds, consumerID)
		}
	}
	soon := now.Add(api.ConsumerOverdueHeldSyncInterval).UTC()
	for consumerID, hold := range due {
		if prior, ok := m.apiConsumerOverdueHolds[consumerID]; ok {
			hold.Since = prior.Since
		} else {
			placed = append(placed, hold)
		}
		m.apiConsumerOverdueHolds[consumerID] = hold
		if e := m.statementInvoiceExports[hold.ExportID]; e.NextStatusSyncAt != nil && e.NextStatusSyncAt.After(soon) {
			e.NextStatusSyncAt = &soon
			m.statementInvoiceExports[hold.ExportID] = e
		}
	}
	return placed, lifted, nil
}

func (m *MemStore) ListAPIConsumerOverdueHolds(_ context.Context, accountID, appID string) ([]APIConsumerOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerOverdueHold{}
	for _, hold := range m.apiConsumerOverdueHolds {
		if hold.AccountID == accountID && hold.AppID == appID {
			out = append(out, hold)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	return out, nil
}

func (m *MemStore) apiConsumerOverdueHold(consumerID string) (APIConsumerOverdueHold, bool) {
	hold, ok := m.apiConsumerOverdueHolds[consumerID]
	return hold, ok
}

// --- PgStore ---

const apiConsumerBillingPolicyCols = `account_id::text, app_id::text, block_overdue_after_days, created_at, updated_at`

func scanAPIConsumerBillingPolicy(row pgx.Row) (APIConsumerBillingPolicy, error) {
	var p APIConsumerBillingPolicy
	err := row.Scan(&p.AccountID, &p.AppID, &p.BlockOverdueAfterDays, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerBillingPolicy{}, ErrNotFound
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func (s *PgStore) PutAPIConsumerBillingPolicy(ctx context.Context, p APIConsumerBillingPolicy) (APIConsumerBillingPolicy, error) {
	if err := validateAPIConsumerBillingPolicy(p); err != nil {
		return APIConsumerBillingPolicy{}, err
	}
	out, err := scanAPIConsumerBillingPolicy(s.pool.QueryRow(ctx, `insert into api_consumer_billing_policies
			(account_id, app_id, block_overdue_after_days)
		select a.account_id, a.id, $3 from apps a where a.account_id = $1::uuid and a.id = $2::uuid
		on conflict (app_id) do update set block_overdue_after_days = excluded.block_overdue_after_days, updated_at = now()
		returning `+apiConsumerBillingPolicyCols, p.AccountID, p.AppID, p.BlockOverdueAfterDays))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerBillingPolicy{}, fmt.Errorf("put consumer billing policy: %w", err)
	}
	return out, err
}

func (s *PgStore) GetAPIConsumerBillingPolicy(ctx context.Context, accountID, appID string) (APIConsumerBillingPolicy, error) {
	return scanAPIConsumerBillingPolicy(s.pool.QueryRow(ctx, `select `+apiConsumerBillingPolicyCols+`
		from api_consumer_billing_policies where account_id = $1::uuid and app_id = $2::uuid`, accountID, appID))
}

func (s *PgStore) DeleteAPIConsumerBillingPolicy(ctx context.Context, accountID, appID string) error {
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_billing_policies where account_id = $1::uuid and app_id = $2::uuid`, accountID, appID)
	if err != nil {
		return fmt.Errorf("delete consumer billing policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SyncAPIConsumerOverdueHolds recomputes every hold in one statement under a
// transaction advisory lock, so apid replicas never report a change twice.
func (s *PgStore) SyncAPIConsumerOverdueHolds(ctx context.Context, now time.Time) ([]APIConsumerOverdueHold, []APIConsumerOverdueHold, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtext('api_consumer_overdue_holds'))`).Scan(&locked); err != nil || !locked {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		with due as (
			select distinct on (s.consumer_id) s.account_id, s.app_id, s.consumer_id, e.id as export_id, e.due_at
			  from usage_statement_invoice_exports e
			  join api_consumer_usage_statements s on s.id = e.statement_id
			  join api_consumer_billing_policies p on p.app_id = s.app_id and p.account_id = s.account_id
			 where e.source_kind = 'app_consumer' and e.status = 'completed'
			   and e.payment_status in ('open', 'uncollectible') and e.due_at is not null
			   and coalesce(e.amount_remaining_minor, 1) > 0
			   and e.due_at + make_interval(days => p.block_overdue_after_days) <= $1
			   and e.status_synced_at >= $2
			 order by s.consumer_id, e.due_at
		), placed as (
			insert into api_consumer_overdue_holds (consumer_id, account_id, app_id, export_id, due_at, since)
			select consumer_id, account_id, app_id, export_id, due_at, $1 from due
			on conflict (consumer_id) do update set export_id = excluded.export_id, due_at = excluded.due_at
			returning consumer_id, account_id, app_id, export_id, due_at, since, (xmax = 0) as inserted
		), lifted as (
			delete from api_consumer_overdue_holds h
			 where not exists (select 1 from due d where d.consumer_id = h.consumer_id)
			returning h.consumer_id, h.account_id, h.app_id, h.export_id, h.due_at, h.since
		)
		select true, consumer_id::text, account_id::text, app_id::text, export_id::text, due_at, since from placed where inserted
		union all
		select false, consumer_id::text, account_id::text, app_id::text, export_id::text, due_at, since from lifted`,
		now.UTC(), now.Add(-api.ConsumerOverdueStatusMaxAge).UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("sync overdue holds: %w", err)
	}
	var placed, lifted []APIConsumerOverdueHold
	for rows.Next() {
		var isPlaced bool
		var h APIConsumerOverdueHold
		if err := rows.Scan(&isPlaced, &h.ConsumerID, &h.AccountID, &h.AppID, &h.ExportID, &h.DueAt, &h.Since); err != nil {
			rows.Close()
			return nil, nil, err
		}
		h.DueAt, h.Since = h.DueAt.UTC(), h.Since.UTC()
		if isPlaced {
			placed = append(placed, h)
		} else {
			lifted = append(lifted, h)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `update usage_statement_invoice_exports e set next_status_sync_at = $1
		 where e.next_status_sync_at > $1 and e.id in (select export_id from api_consumer_overdue_holds)`,
		now.Add(api.ConsumerOverdueHeldSyncInterval).UTC()); err != nil {
		return nil, nil, fmt.Errorf("resync held invoices: %w", err)
	}
	return placed, lifted, tx.Commit(ctx)
}

func (s *PgStore) ListAPIConsumerOverdueHolds(ctx context.Context, accountID, appID string) ([]APIConsumerOverdueHold, error) {
	rows, err := s.pool.Query(ctx, `select consumer_id::text, account_id::text, app_id::text, export_id::text, due_at, since
		from api_consumer_overdue_holds where account_id = $1::uuid and app_id = $2::uuid order by due_at`, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerOverdueHold{}
	for rows.Next() {
		var h APIConsumerOverdueHold
		if err := rows.Scan(&h.ConsumerID, &h.AccountID, &h.AppID, &h.ExportID, &h.DueAt, &h.Since); err != nil {
			return nil, err
		}
		h.DueAt, h.Since = h.DueAt.UTC(), h.Since.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

// apiConsumerOverdueHold reads one consumer's hold for its plan policy.
func (s *PgStore) apiConsumerOverdueHold(ctx context.Context, accountID, consumerID string) (APIConsumerOverdueHold, bool, error) {
	var h APIConsumerOverdueHold
	err := s.pool.QueryRow(ctx, `select consumer_id::text, account_id::text, app_id::text, export_id::text, due_at, since
		from api_consumer_overdue_holds where account_id = $1::uuid and consumer_id = $2::uuid`, accountID, consumerID).
		Scan(&h.ConsumerID, &h.AccountID, &h.AppID, &h.ExportID, &h.DueAt, &h.Since)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerOverdueHold{}, false, nil
	}
	if err != nil {
		return APIConsumerOverdueHold{}, false, err
	}
	h.DueAt, h.Since = h.DueAt.UTC(), h.Since.UTC()
	return h, true, nil
}
