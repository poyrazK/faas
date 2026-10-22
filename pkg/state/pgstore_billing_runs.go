package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const billingRunCols = `id::text, account_id::text, period_start, period_end, sources, finalize, export_provider, hold_on_gaps,
	trigger, status, discovered_at, coalesce(lease_owner, ''), lease_until, created_at, started_at, completed_at, notified_at,
	product_discovered_at`

func scanBillingRun(row pgx.Row) (BillingRun, error) {
	var r BillingRun
	err := row.Scan(&r.ID, &r.AccountID, &r.PeriodStart, &r.PeriodEnd, &r.Sources, &r.Finalize, &r.Export, &r.HoldOnGaps,
		&r.Trigger, &r.Status, &r.DiscoveredAt, &r.LeaseOwner, &r.LeaseUntil, &r.CreatedAt, &r.StartedAt, &r.CompletedAt, &r.NotifiedAt,
		&r.ProductDiscoveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingRun{}, ErrNotFound
	}
	return r, err
}

const billingRunItemCols = `run_id::text, source_kind, source_id::text, coalesce(app_id::text, ''), external_ref, outcome,
	coalesce(statement_id::text, ''), coalesce(revision, 0), coalesce(currency, ''), coalesce(amount_millicents, 0),
	coalesce(completeness, ''), coalesce(invoice_id, ''), exported_count, coalesce(reason, ''), attempts, next_attempt_at, updated_at,
	coalesce((select e.payment_status from usage_statement_invoice_exports e
	           where e.source_kind = billing_run_items.source_kind and e.statement_id = billing_run_items.statement_id), '')`

func scanBillingRunItems(rows pgx.Rows) ([]BillingRunItem, error) {
	defer rows.Close()
	out := []BillingRunItem{}
	for rows.Next() {
		var i BillingRunItem
		if err := rows.Scan(&i.RunID, &i.SourceKind, &i.SourceID, &i.AppID, &i.ExternalRef, &i.Outcome, &i.StatementID, &i.Revision,
			&i.Currency, &i.AmountMillicents, &i.Completeness, &i.InvoiceID, &i.ExportedCount, &i.Reason, &i.Attempts,
			&i.NextAttemptAt, &i.UpdatedAt, &i.PaymentStatus); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// fillBillingRunCounts sets Counts on each run from its items' outcomes.
func (s *PgStore) fillBillingRunCounts(ctx context.Context, runs []BillingRun) error {
	if len(runs) == 0 {
		return nil
	}
	ids, index := make([]string, len(runs)), map[string]int{}
	for i := range runs {
		ids[i], index[runs[i].ID] = runs[i].ID, i
		runs[i].Counts = map[string]int{}
	}
	rows, err := s.pool.Query(ctx, `select run_id::text, outcome, count(*) from billing_run_items
		where run_id = any($1::uuid[]) group by run_id, outcome`, ids)
	if err != nil {
		return fmt.Errorf("count billing run items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID, outcome string
		var n int
		if err := rows.Scan(&runID, &outcome, &n); err != nil {
			return err
		}
		runs[index[runID]].Counts[outcome] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.addBillingRunProductCounts(ctx, runs, index)
}

func (s *PgStore) billingRunWithCounts(ctx context.Context, run BillingRun) (BillingRun, error) {
	runs := []BillingRun{run}
	if err := s.fillBillingRunCounts(ctx, runs); err != nil {
		return BillingRun{}, err
	}
	return runs[0], nil
}

type billingRunQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insertBillingRun(ctx context.Context, q billingRunQuerier, run BillingRun) (BillingRun, error) {
	return scanBillingRun(q.QueryRow(ctx, `insert into billing_runs
			(account_id, period_start, period_end, sources, finalize, export_provider, hold_on_gaps, trigger)
		values ($1::uuid, $2, $3, $4, $5, $6, $7, $8) returning `+billingRunCols,
		run.AccountID, run.PeriodStart, run.PeriodEnd, run.Sources, run.Finalize, run.Export, run.HoldOnGaps, run.Trigger))
}

func (s *PgStore) activeBillingRun(ctx context.Context, accountID string) (BillingRun, error) {
	run, err := scanBillingRun(s.pool.QueryRow(ctx, `select `+billingRunCols+` from billing_runs
		where account_id = $1::uuid and status in ('queued', 'running')`, accountID))
	if err != nil {
		return BillingRun{}, err
	}
	return s.billingRunWithCounts(ctx, run)
}

func (s *PgStore) CreateBillingRun(ctx context.Context, run BillingRun) (BillingRun, error) {
	if err := validateBillingRun(run); err != nil {
		return BillingRun{}, err
	}
	created, err := insertBillingRun(ctx, s.pool, run)
	if isUniqueViolation(err) {
		active, activeErr := s.activeBillingRun(ctx, run.AccountID)
		if activeErr != nil {
			return BillingRun{}, fmt.Errorf("read active billing run: %w", activeErr)
		}
		return active, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("create billing run: %w", err)
	}
	return s.billingRunWithCounts(ctx, created)
}

func (s *PgStore) GetBillingRun(ctx context.Context, accountID, runID string) (BillingRun, error) {
	run, err := scanBillingRun(s.pool.QueryRow(ctx, `select `+billingRunCols+` from billing_runs
		where id = $1::uuid and account_id = $2::uuid`, runID, accountID))
	if err != nil {
		return BillingRun{}, err
	}
	return s.billingRunWithCounts(ctx, run)
}

func (s *PgStore) ListBillingRuns(ctx context.Context, accountID string, limit int) ([]BillingRun, error) {
	rows, err := s.pool.Query(ctx, `select `+billingRunCols+` from billing_runs
		where account_id = $1::uuid order by created_at desc, id desc limit $2`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("list billing runs: %w", err)
	}
	runs := []BillingRun{}
	for rows.Next() {
		run, err := scanBillingRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, s.fillBillingRunCounts(ctx, runs)
}

func (s *PgStore) ListBillingRunItems(ctx context.Context, accountID, runID, outcome, after string, limit int) ([]BillingRunItem, error) {
	if _, err := scanBillingRun(s.pool.QueryRow(ctx, `select `+billingRunCols+` from billing_runs
		where id = $1::uuid and account_id = $2::uuid`, runID, accountID)); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+billingRunItemCols+` from billing_run_items
		where run_id = $1::uuid and ($2 = '' or outcome = $2) and source_kind || '/' || source_id::text > $3
		order by source_kind || '/' || source_id::text limit $4`, runID, outcome, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list billing run items: %w", err)
	}
	items, err := scanBillingRunItems(rows)
	if err != nil || len(items) == limit {
		return items, err
	}
	// Product Control items sort after every other kind.
	products, err := s.billingRunProductItems(ctx, runID, outcome, after, limit-len(items))
	return append(items, products...), err
}

func (s *PgStore) ClaimBillingRun(ctx context.Context, owner string, now time.Time, ttl time.Duration) (BillingRun, error) {
	run, err := scanBillingRun(s.pool.QueryRow(ctx, `update billing_runs
		   set status = 'running', lease_owner = $1, lease_until = $3, started_at = coalesce(started_at, $2)
		 where id = (select id from billing_runs
		              where status in ('queued', 'running') and (lease_until is null or lease_until <= $2)
		              order by created_at limit 1 for update skip locked)
		returning `+billingRunCols, owner, now, now.Add(ttl)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return BillingRun{}, err
		}
		return BillingRun{}, fmt.Errorf("claim billing run: %w", err)
	}
	return s.billingRunWithCounts(ctx, run)
}

// lockLeasedBillingRun locks the run row and checks owner still leases it.
func lockLeasedBillingRun(ctx context.Context, tx pgx.Tx, runID, owner string) (BillingRun, error) {
	run, err := scanBillingRun(tx.QueryRow(ctx, `select `+billingRunCols+` from billing_runs where id = $1::uuid for update`, runID))
	if err != nil {
		return BillingRun{}, err
	}
	if run.LeaseOwner != owner || run.Status == BillingRunCompleted {
		return BillingRun{}, ErrBillingRunLeaseLost
	}
	return run, nil
}

func (s *PgStore) DiscoverBillingRunItems(ctx context.Context, runID, owner, tenantReason string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := lockLeasedBillingRun(ctx, tx, runID, owner)
	if err != nil || run.Discovered() {
		return err
	}
	if run.DiscoveredAt == nil {
		if err := discoverBillingRunUsageItems(ctx, tx, run, tenantReason, now); err != nil {
			return err
		}
	}
	if run.Includes(BillingRunSourceProductCustomers) && run.ProductDiscoveredAt == nil {
		if err := discoverBillingRunProductItems(ctx, tx, run, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// discoverBillingRunUsageItems adds app consumers and platform tenants with
// billable usage in the run's month (ADR-1503).
func discoverBillingRunUsageItems(ctx context.Context, tx pgx.Tx, run BillingRun, tenantReason string, now time.Time) error {
	runID := run.ID
	if run.Includes(BillingRunSourceAppConsumers) {
		// Usage attributed to a platform tenant is billed on the tenant's
		// statement when the run includes platform tenants (ADR-1503).
		if _, err := tx.Exec(ctx, `with attributed as (
				select distinct app_id, consumer_key from platform_tenant_usage_minutes
				 where account_id = $2::uuid and source_kind = 'consumer' and billable_units > 0
				   and window_start >= $3 and window_start < $4),
			billed as (
				select distinct app_id, consumer_key from api_consumer_usage_minutes
				 where account_id = $2::uuid and consumer_key <> '__anonymous__' and billable_units > 0
				   and window_start >= $3 and window_start < $4
				union
				-- A card with a base fee or minimum bills consumers without
				-- usage too (ADR-1316).
				select c.app_id, c.id::text from api_consumers c
				 where c.account_id = $2::uuid and c.created_at < $4 and (c.revoked_at is null or c.revoked_at >= $3)
				   and exists (select 1 from api_consumer_rate_cards r
				                where r.app_id = c.app_id and r.effective_from < $4
				                  and (r.base_fee_millicents_per_month > 0 or r.minimum_millicents_per_month > 0)))
			insert into billing_run_items (run_id, source_kind, source_id, app_id, external_ref, outcome, reason, next_attempt_at, updated_at)
			select $1::uuid, 'app_consumer', c.id, c.app_id, c.external_ref,
			       case when $5::boolean and a.app_id is not null then 'skipped' else 'pending' end,
			       case when $5::boolean and a.app_id is not null then $6::text end, $7::timestamptz, $7::timestamptz
			  from billed b
			  join api_consumers c on c.account_id = $2::uuid and c.app_id = b.app_id and c.id::text = b.consumer_key
			  left join attributed a on a.app_id = b.app_id and a.consumer_key = b.consumer_key
			on conflict do nothing`,
			runID, run.AccountID, run.PeriodStart, run.PeriodEnd, run.Includes(BillingRunSourcePlatformTenants), tenantReason, now); err != nil {
			return fmt.Errorf("discover app consumers: %w", err)
		}
	}
	if run.Includes(BillingRunSourcePlatformTenants) {
		if _, err := tx.Exec(ctx, `insert into billing_run_items (run_id, source_kind, source_id, external_ref, outcome, next_attempt_at, updated_at)
			select $1::uuid, 'platform_tenant', p.id, p.external_ref, 'pending', $5::timestamptz, $5::timestamptz
			  from platform_tenants p
			 where p.account_id = $2::uuid
			   and (exists (select 1 from platform_tenant_usage_minutes t
			                 where t.account_id = $2::uuid and t.platform_tenant_id = p.id and t.billable_units > 0
			                   and t.window_start >= $3 and t.window_start < $4)
			        -- A card with a base fee or minimum bills tenants without
			        -- usage too (ADR-1322).
			        or (p.created_at < $4 and exists (select 1 from platform_tenant_rate_cards r
			             where r.account_id = $2::uuid and r.platform_tenant_id = p.id and r.effective_from < $4
			               and (r.base_fee_millicents_per_month > 0 or r.minimum_millicents_per_month > 0))))
			on conflict do nothing`, runID, run.AccountID, run.PeriodStart, run.PeriodEnd, now); err != nil {
			return fmt.Errorf("discover platform tenants: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `update billing_runs set discovered_at = $2 where id = $1::uuid`, runID, now); err != nil {
		return fmt.Errorf("mark billing run discovered: %w", err)
	}
	return nil
}

func (s *PgStore) ListDueBillingRunItems(ctx context.Context, runID, owner string, now time.Time, limit int) ([]BillingRunItem, error) {
	var leased bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from billing_runs
		where id = $1::uuid and lease_owner = $2 and status <> 'completed')`, runID, owner).Scan(&leased); err != nil {
		return nil, fmt.Errorf("check billing run lease: %w", err)
	}
	if !leased {
		return nil, ErrBillingRunLeaseLost
	}
	// Items are processed in descending Key order; Product Control keys
	// sort after every other kind.
	items, err := s.dueBillingRunProductItems(ctx, runID, now, limit)
	if err != nil || len(items) == limit {
		return items, err
	}
	rows, err := s.pool.Query(ctx, `select `+billingRunItemCols+` from billing_run_items
		where run_id = $1::uuid and outcome = 'pending' and next_attempt_at <= $2
		order by source_kind desc, source_id desc limit $3`, runID, now, limit-len(items))
	if err != nil {
		return nil, fmt.Errorf("list due billing run items: %w", err)
	}
	usage, err := scanBillingRunItems(rows)
	return append(items, usage...), err
}

func (s *PgStore) RenewBillingRunLease(ctx context.Context, runID, owner string, until time.Time) error {
	tag, err := s.pool.Exec(ctx, `update billing_runs set lease_until = greatest(lease_until, $3)
		where id = $1::uuid and lease_owner = $2 and status <> 'completed'`, runID, owner, until.UTC())
	if err != nil {
		return fmt.Errorf("renew billing run lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBillingRunLeaseLost
	}
	return nil
}

func (s *PgStore) UpdateBillingRunItem(ctx context.Context, owner string, item BillingRunItem) error {
	if !validBillingRunItemOutcome(item.Outcome) || len(item.Reason) > 1000 {
		return fmt.Errorf("%w: invalid billing run item", ErrInvalidArgument)
	}
	if item.SourceKind == StatementSourceProductControl {
		return s.updateBillingRunProductItem(ctx, owner, item)
	}
	tag, err := s.pool.Exec(ctx, `update billing_run_items i
		   set outcome = $4, statement_id = nullif($5, '')::uuid, revision = nullif($6, 0), currency = nullif($7, ''),
		       amount_millicents = case when $5::text = '' then null else $8::bigint end, completeness = nullif($9, ''),
		       invoice_id = nullif($10, ''), exported_count = $11, reason = nullif($12, ''), attempts = $13,
		       next_attempt_at = $14, updated_at = now()
		  from billing_runs r
		 where r.id = i.run_id and r.lease_owner = $15 and r.status <> 'completed'
		   and i.run_id = $1::uuid and i.source_kind = $2 and i.source_id = $3::uuid`,
		item.RunID, item.SourceKind, item.SourceID, item.Outcome, item.StatementID, item.Revision, item.Currency,
		item.AmountMillicents, item.Completeness, item.InvoiceID, item.ExportedCount, item.Reason, item.Attempts,
		item.NextAttemptAt, owner)
	if err != nil {
		return fmt.Errorf("update billing run item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBillingRunLeaseLost
	}
	return nil
}

func (s *PgStore) ReleaseBillingRun(ctx context.Context, runID, owner string, now time.Time) (BillingRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BillingRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockLeasedBillingRun(ctx, tx, runID, owner); err != nil {
		return BillingRun{}, err
	}
	// A run completes once every included source is discovered and no item
	// of either table is pending.
	run, err := scanBillingRun(tx.QueryRow(ctx, `with done as (
			select r.discovered_at is not null
			       and (not ('product_customers' = any(r.sources)) or r.product_discovered_at is not null)
			       and not exists (select 1 from billing_run_items where run_id = r.id and outcome = 'pending')
			       and not exists (select 1 from billing_run_product_items where run_id = r.id and outcome = 'pending') as done
			  from billing_runs r where r.id = $1::uuid)
		update billing_runs
		   set lease_owner = null, lease_until = null,
		       status = case when (select done from done) then 'completed' else status end,
		       completed_at = case when (select done from done) then $2 else completed_at end
		 where id = $1::uuid returning `+billingRunCols, runID, now))
	if err != nil {
		return BillingRun{}, fmt.Errorf("release billing run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingRun{}, err
	}
	return s.billingRunWithCounts(ctx, run)
}

const billingRunScheduleCols = `account_id::text, sources, finalize, export_provider, hold_on_gaps, delay_hours,
	next_period_start, next_run_at, last_period_start, created_at, updated_at`

func scanBillingRunSchedule(row pgx.Row) (BillingRunSchedule, error) {
	var s BillingRunSchedule
	err := row.Scan(&s.AccountID, &s.Sources, &s.Finalize, &s.Export, &s.HoldOnGaps, &s.DelayHours,
		&s.NextPeriodStart, &s.NextRunAt, &s.LastPeriodStart, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingRunSchedule{}, ErrNotFound
	}
	return s, err
}

func (s *PgStore) PutBillingRunSchedule(ctx context.Context, sched BillingRunSchedule) (BillingRunSchedule, error) {
	if err := validateBillingRunSchedule(sched); err != nil {
		return BillingRunSchedule{}, err
	}
	out, err := scanBillingRunSchedule(s.pool.QueryRow(ctx, `insert into billing_run_schedules
			(account_id, sources, finalize, export_provider, hold_on_gaps, delay_hours, next_period_start, next_run_at)
		values ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
		on conflict (account_id) do update set sources = excluded.sources, finalize = excluded.finalize,
			export_provider = excluded.export_provider, hold_on_gaps = excluded.hold_on_gaps,
			delay_hours = excluded.delay_hours, next_period_start = excluded.next_period_start,
			next_run_at = excluded.next_run_at, updated_at = now()
		returning `+billingRunScheduleCols,
		sched.AccountID, sched.Sources, sched.Finalize, sched.Export, sched.HoldOnGaps, sched.DelayHours, sched.NextPeriodStart, sched.NextRunAt))
	if err != nil {
		return BillingRunSchedule{}, fmt.Errorf("put billing run schedule: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetBillingRunSchedule(ctx context.Context, accountID string) (BillingRunSchedule, error) {
	return scanBillingRunSchedule(s.pool.QueryRow(ctx, `select `+billingRunScheduleCols+` from billing_run_schedules
		where account_id = $1::uuid`, accountID))
}

func (s *PgStore) DeleteBillingRunSchedule(ctx context.Context, accountID string) error {
	tag, err := s.pool.Exec(ctx, `delete from billing_run_schedules where account_id = $1::uuid`, accountID)
	if err != nil {
		return fmt.Errorf("delete billing run schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListDueBillingRunSchedules(ctx context.Context, now time.Time, limit int) ([]BillingRunSchedule, error) {
	rows, err := s.pool.Query(ctx, `select `+billingRunScheduleCols+` from billing_run_schedules
		where next_run_at <= $1 order by next_run_at limit $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due billing run schedules: %w", err)
	}
	defer rows.Close()
	out := []BillingRunSchedule{}
	for rows.Next() {
		sched, err := scanBillingRunSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sched)
	}
	return out, rows.Err()
}

func (s *PgStore) StartScheduledBillingRun(ctx context.Context, sched BillingRunSchedule, run BillingRun, next BillingRunSchedule) (BillingRun, error) {
	if err := validateBillingRun(run); err != nil {
		return BillingRun{}, err
	}
	if err := validateBillingRunSchedule(next); err != nil {
		return BillingRun{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BillingRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current time.Time
	err = tx.QueryRow(ctx, `select next_period_start from billing_run_schedules where account_id = $1::uuid for update`, sched.AccountID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !current.Equal(sched.NextPeriodStart)) {
		return BillingRun{}, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("lock billing run schedule: %w", err)
	}
	created, err := scanBillingRun(tx.QueryRow(ctx, `select `+billingRunCols+` from billing_runs
		where account_id = $1::uuid and trigger = 'schedule' and period_start = $2`, run.AccountID, run.PeriodStart))
	if errors.Is(err, ErrNotFound) {
		created, err = insertBillingRun(ctx, tx, run)
	}
	if isUniqueViolation(err) {
		return BillingRun{}, ErrConflict
	}
	if err != nil {
		return BillingRun{}, fmt.Errorf("start scheduled billing run: %w", err)
	}
	if _, err := tx.Exec(ctx, `update billing_run_schedules
		   set last_period_start = $2, next_period_start = $3, next_run_at = $4, updated_at = now()
		 where account_id = $1::uuid`, sched.AccountID, sched.NextPeriodStart, next.NextPeriodStart, next.NextRunAt); err != nil {
		return BillingRun{}, fmt.Errorf("advance billing run schedule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BillingRun{}, err
	}
	return s.billingRunWithCounts(ctx, created)
}

func (s *PgStore) ListBillingRunsToNotify(ctx context.Context, limit int) ([]BillingRun, error) {
	rows, err := s.pool.Query(ctx, `select `+billingRunCols+` from billing_runs
		where status = 'completed' and notified_at is null order by completed_at limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list billing runs to notify: %w", err)
	}
	runs := []BillingRun{}
	for rows.Next() {
		run, err := scanBillingRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, s.fillBillingRunCounts(ctx, runs)
}

func (s *PgStore) MarkBillingRunNotified(ctx context.Context, runID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `update billing_runs set notified_at = coalesce(notified_at, $2) where id = $1::uuid`, runID, at)
	if err != nil {
		return fmt.Errorf("mark billing run notified: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
