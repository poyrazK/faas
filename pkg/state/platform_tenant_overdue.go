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

// PlatformTenantBillingPolicy is an account's rule for unpaid tenant
// invoices (ADR-1324): a platform tenant with an exported invoice still
// unpaid BlockOverdueAfterDays after its due date is refused at the gateway
// until it is paid or voided.
type PlatformTenantBillingPolicy struct {
	AccountID             string
	BlockOverdueAfterDays int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// PlatformTenantOverdueHold blocks one tenant because of its oldest overdue
// invoice. Since is when the hold was placed.
type PlatformTenantOverdueHold struct {
	AccountID string
	TenantID  string
	ExportID  string
	DueAt     time.Time
	Since     time.Time
}

// PlatformTenantOverdueStore persists tenant overdue policies and holds
// (ADR-1324).
type PlatformTenantOverdueStore interface {
	PutPlatformTenantBillingPolicy(ctx context.Context, p PlatformTenantBillingPolicy) (PlatformTenantBillingPolicy, error)
	// GetPlatformTenantBillingPolicy is ErrNotFound when the account has none.
	GetPlatformTenantBillingPolicy(ctx context.Context, accountID string) (PlatformTenantBillingPolicy, error)
	// DeletePlatformTenantBillingPolicy removes the policy; the next sync
	// lifts its holds.
	DeletePlatformTenantBillingPolicy(ctx context.Context, accountID string) error
	// SyncPlatformTenantOverdueHolds places a hold on every tenant whose
	// oldest qualifying invoice is overdue at now and lifts every other hold,
	// as SyncAPIConsumerOverdueHolds does for consumers.
	SyncPlatformTenantOverdueHolds(ctx context.Context, now time.Time) (placed, lifted []PlatformTenantOverdueHold, err error)
	ListPlatformTenantOverdueHolds(ctx context.Context, accountID string) ([]PlatformTenantOverdueHold, error)
}

var (
	_ PlatformTenantOverdueStore = (*MemStore)(nil)
	_ PlatformTenantOverdueStore = (*PgStore)(nil)
)

func validatePlatformTenantBillingPolicy(p PlatformTenantBillingPolicy) error {
	if _, err := uuid.Parse(p.AccountID); err != nil {
		return fmt.Errorf("%w: account_id must be a UUID", ErrInvalidArgument)
	}
	if p.BlockOverdueAfterDays < 0 || p.BlockOverdueAfterDays > api.ConsumerOverdueBlockMaxDays {
		return fmt.Errorf("%w: block_overdue_after_days must be 0 to %d", ErrInvalidArgument, api.ConsumerOverdueBlockMaxDays)
	}
	return nil
}

// --- MemStore ---

func (m *MemStore) PutPlatformTenantBillingPolicy(_ context.Context, p PlatformTenantBillingPolicy) (PlatformTenantBillingPolicy, error) {
	if err := validatePlatformTenantBillingPolicy(p); err != nil {
		return PlatformTenantBillingPolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.platformTenantBillingPolicies == nil {
		m.platformTenantBillingPolicies = map[string]PlatformTenantBillingPolicy{}
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	if prior, ok := m.platformTenantBillingPolicies[p.AccountID]; ok {
		p.CreatedAt = prior.CreatedAt
	}
	m.platformTenantBillingPolicies[p.AccountID] = p
	return p, nil
}

func (m *MemStore) GetPlatformTenantBillingPolicy(_ context.Context, accountID string) (PlatformTenantBillingPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.platformTenantBillingPolicies[accountID]
	if !ok {
		return PlatformTenantBillingPolicy{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) DeletePlatformTenantBillingPolicy(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.platformTenantBillingPolicies[accountID]; !ok {
		return ErrNotFound
	}
	delete(m.platformTenantBillingPolicies, accountID)
	return nil
}

func (m *MemStore) SyncPlatformTenantOverdueHolds(_ context.Context, now time.Time) ([]PlatformTenantOverdueHold, []PlatformTenantOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	due := map[string]PlatformTenantOverdueHold{}
	for _, e := range m.statementInvoiceExports {
		if e.SourceKind != StatementSourcePlatformTenant {
			continue
		}
		statement, ok := m.platformTenantStatements[e.StatementID]
		if !ok {
			continue
		}
		policy, ok := m.platformTenantBillingPolicies[statement.AccountID]
		if !ok || !overdueExport(e, policy.BlockOverdueAfterDays, now) {
			continue
		}
		if prior, ok := due[statement.TenantID]; ok && !e.DueAt.Before(prior.DueAt) {
			continue
		}
		due[statement.TenantID] = PlatformTenantOverdueHold{AccountID: statement.AccountID, TenantID: statement.TenantID,
			ExportID: e.ID, DueAt: e.DueAt.UTC(), Since: now.UTC()}
	}
	if m.platformTenantOverdueHolds == nil {
		m.platformTenantOverdueHolds = map[string]PlatformTenantOverdueHold{}
	}
	var placed, lifted []PlatformTenantOverdueHold
	for tenantID, hold := range m.platformTenantOverdueHolds {
		if _, still := due[tenantID]; !still {
			lifted = append(lifted, hold)
			delete(m.platformTenantOverdueHolds, tenantID)
		}
	}
	soon := now.Add(api.ConsumerOverdueHeldSyncInterval).UTC()
	for tenantID, hold := range due {
		if prior, ok := m.platformTenantOverdueHolds[tenantID]; ok {
			hold.Since = prior.Since
		} else {
			placed = append(placed, hold)
		}
		m.platformTenantOverdueHolds[tenantID] = hold
		if e := m.statementInvoiceExports[hold.ExportID]; e.NextStatusSyncAt != nil && e.NextStatusSyncAt.After(soon) {
			e.NextStatusSyncAt = &soon
			m.statementInvoiceExports[hold.ExportID] = e
		}
	}
	return placed, lifted, nil
}

func (m *MemStore) ListPlatformTenantOverdueHolds(_ context.Context, accountID string) ([]PlatformTenantOverdueHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlatformTenantOverdueHold{}
	for _, hold := range m.platformTenantOverdueHolds {
		if hold.AccountID == accountID {
			out = append(out, hold)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	return out, nil
}

// --- PgStore ---

const platformTenantBillingPolicyCols = `account_id::text, block_overdue_after_days, created_at, updated_at`

func scanPlatformTenantBillingPolicy(row pgx.Row) (PlatformTenantBillingPolicy, error) {
	var p PlatformTenantBillingPolicy
	err := row.Scan(&p.AccountID, &p.BlockOverdueAfterDays, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantBillingPolicy{}, ErrNotFound
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, err
}

func (s *PgStore) PutPlatformTenantBillingPolicy(ctx context.Context, p PlatformTenantBillingPolicy) (PlatformTenantBillingPolicy, error) {
	if err := validatePlatformTenantBillingPolicy(p); err != nil {
		return PlatformTenantBillingPolicy{}, err
	}
	out, err := scanPlatformTenantBillingPolicy(s.pool.QueryRow(ctx, `insert into platform_tenant_billing_policies
			(account_id, block_overdue_after_days) values ($1::uuid, $2)
		on conflict (account_id) do update set block_overdue_after_days = excluded.block_overdue_after_days, updated_at = now()
		returning `+platformTenantBillingPolicyCols, p.AccountID, p.BlockOverdueAfterDays))
	if err != nil {
		return PlatformTenantBillingPolicy{}, fmt.Errorf("put platform tenant billing policy: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetPlatformTenantBillingPolicy(ctx context.Context, accountID string) (PlatformTenantBillingPolicy, error) {
	return scanPlatformTenantBillingPolicy(s.pool.QueryRow(ctx, `select `+platformTenantBillingPolicyCols+`
		from platform_tenant_billing_policies where account_id = $1::uuid`, accountID))
}

func (s *PgStore) DeletePlatformTenantBillingPolicy(ctx context.Context, accountID string) error {
	tag, err := s.pool.Exec(ctx, `delete from platform_tenant_billing_policies where account_id = $1::uuid`, accountID)
	if err != nil {
		return fmt.Errorf("delete platform tenant billing policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SyncPlatformTenantOverdueHolds recomputes every tenant hold in one
// statement under a transaction advisory lock.
func (s *PgStore) SyncPlatformTenantOverdueHolds(ctx context.Context, now time.Time) ([]PlatformTenantOverdueHold, []PlatformTenantOverdueHold, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtext('platform_tenant_overdue_holds'))`).Scan(&locked); err != nil || !locked {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		with due as (
			select distinct on (s.platform_tenant_id) s.account_id, s.platform_tenant_id, e.id as export_id, e.due_at
			  from usage_statement_invoice_exports e
			  join platform_tenant_statements s on s.id = e.statement_id
			  join platform_tenant_billing_policies p on p.account_id = s.account_id
			 where e.source_kind = 'platform_tenant' and e.status = 'completed'
			   and e.payment_status in ('open', 'uncollectible') and e.due_at is not null
			   and coalesce(e.amount_remaining_minor, 1) > 0
			   and e.due_at + make_interval(days => p.block_overdue_after_days) <= $1
			 order by s.platform_tenant_id, e.due_at
		), placed as (
			insert into platform_tenant_overdue_holds (platform_tenant_id, account_id, export_id, due_at, since)
			select platform_tenant_id, account_id, export_id, due_at, $1 from due
			on conflict (platform_tenant_id) do update set export_id = excluded.export_id, due_at = excluded.due_at
			returning platform_tenant_id, account_id, export_id, due_at, since, (xmax = 0) as inserted
		), lifted as (
			delete from platform_tenant_overdue_holds h
			 where not exists (select 1 from due d where d.platform_tenant_id = h.platform_tenant_id)
			returning h.platform_tenant_id, h.account_id, h.export_id, h.due_at, h.since
		)
		select true, platform_tenant_id::text, account_id::text, export_id::text, due_at, since from placed where inserted
		union all
		select false, platform_tenant_id::text, account_id::text, export_id::text, due_at, since from lifted`, now.UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("sync platform tenant overdue holds: %w", err)
	}
	var placed, lifted []PlatformTenantOverdueHold
	for rows.Next() {
		var isPlaced bool
		var h PlatformTenantOverdueHold
		if err := rows.Scan(&isPlaced, &h.TenantID, &h.AccountID, &h.ExportID, &h.DueAt, &h.Since); err != nil {
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
		 where e.next_status_sync_at > $1 and e.id in (select export_id from platform_tenant_overdue_holds)`,
		now.Add(api.ConsumerOverdueHeldSyncInterval).UTC()); err != nil {
		return nil, nil, fmt.Errorf("resync held tenant invoices: %w", err)
	}
	return placed, lifted, tx.Commit(ctx)
}

func (s *PgStore) ListPlatformTenantOverdueHolds(ctx context.Context, accountID string) ([]PlatformTenantOverdueHold, error) {
	rows, err := s.pool.Query(ctx, `select platform_tenant_id::text, account_id::text, export_id::text, due_at, since
		from platform_tenant_overdue_holds where account_id = $1::uuid order by due_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformTenantOverdueHold{}
	for rows.Next() {
		var h PlatformTenantOverdueHold
		if err := rows.Scan(&h.TenantID, &h.AccountID, &h.ExportID, &h.DueAt, &h.Since); err != nil {
			return nil, err
		}
		h.DueAt, h.Since = h.DueAt.UTC(), h.Since.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

// overdueTenantDecision is the admission refusal for a held tenant
// (ADR-1324).
func overdueTenantDecision(dueAt time.Time) PlatformTenantBudgetDecision {
	return PlatformTenantBudgetDecision{Scope: PlatformTenantBudgetScopeOverdue, OverdueDueAt: dueAt.UTC()}
}
