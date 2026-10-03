package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const platformTenantStatementCols = `id, account_id, platform_tenant_id, period_start, period_end,
       revision, status, currency, billable_units, unpriced_units, amount_millicents,
       lines, coverage, as_of, created_at, finalized_at`
const platformTenantStatementHeaderCols = `id, account_id, platform_tenant_id, period_start, period_end,
       revision, status, currency, billable_units, unpriced_units, amount_millicents,
       lines, as_of, created_at, finalized_at`
const platformTenantStatementSummaryCols = `id, account_id, platform_tenant_id, period_start, period_end,
       revision, status, currency, billable_units, unpriced_units, amount_millicents,
       as_of, created_at, finalized_at`
const platformTenantHandoffCols = `id, account_id, platform_tenant_id, statement_id,
       external_invoice_id, currency, amount_millicents, created_at`

func scanPlatformTenantStatement(row pgx.Row) (PlatformTenantStatement, error) {
	var out PlatformTenantStatement
	var status string
	var rawLines, rawCoverage []byte
	err := row.Scan(&out.ID, &out.AccountID, &out.TenantID, &out.PeriodStart, &out.PeriodEnd,
		&out.Revision, &status, &out.Currency, &out.BillableUnits, &out.UnpricedUnits,
		&out.AmountMillicents, &rawLines, &rawCoverage, &out.AsOf, &out.CreatedAt, &out.FinalizedAt)
	if err != nil {
		return out, err
	}
	out.Status = APIConsumerUsageStatementStatus(status)
	if err := json.Unmarshal(rawLines, &out.Lines); err != nil {
		return out, err
	}
	if err := json.Unmarshal(rawCoverage, &out.Coverage); err != nil {
		return out, err
	}
	out.PeriodStart, out.PeriodEnd, out.AsOf = out.PeriodStart.UTC(), out.PeriodEnd.UTC(), out.AsOf.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	if out.FinalizedAt != nil {
		at := out.FinalizedAt.UTC()
		out.FinalizedAt = &at
	}
	for i := range out.Lines {
		out.Lines[i].WindowStart = out.Lines[i].WindowStart.UTC()
		if out.Lines[i].WindowEnd.IsZero() {
			// Existing snapshots contain one minute per line and predate the
			// compact interval field.
			out.Lines[i].WindowEnd = out.Lines[i].WindowStart.Add(time.Minute)
		} else {
			out.Lines[i].WindowEnd = out.Lines[i].WindowEnd.UTC()
		}
	}
	for i := range out.Coverage {
		out.Coverage[i].WindowStart = out.Coverage[i].WindowStart.UTC()
	}
	return out, nil
}

func scanPlatformTenantStatementHeader(row pgx.Row) (PlatformTenantStatement, error) {
	var out PlatformTenantStatement
	var status string
	var rawLines []byte
	err := row.Scan(&out.ID, &out.AccountID, &out.TenantID, &out.PeriodStart, &out.PeriodEnd,
		&out.Revision, &status, &out.Currency, &out.BillableUnits, &out.UnpricedUnits,
		&out.AmountMillicents, &rawLines, &out.AsOf, &out.CreatedAt, &out.FinalizedAt)
	if err != nil {
		return out, err
	}
	out.Status = APIConsumerUsageStatementStatus(status)
	if err := json.Unmarshal(rawLines, &out.Lines); err != nil {
		return out, err
	}
	out.PeriodStart, out.PeriodEnd, out.AsOf = out.PeriodStart.UTC(), out.PeriodEnd.UTC(), out.AsOf.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	if out.FinalizedAt != nil {
		at := out.FinalizedAt.UTC()
		out.FinalizedAt = &at
	}
	for i := range out.Lines {
		out.Lines[i].WindowStart = out.Lines[i].WindowStart.UTC()
		if out.Lines[i].WindowEnd.IsZero() {
			out.Lines[i].WindowEnd = out.Lines[i].WindowStart.Add(time.Minute)
		} else {
			out.Lines[i].WindowEnd = out.Lines[i].WindowEnd.UTC()
		}
	}
	return out, nil
}

// PlanPlatformTenantStatement reads the latest revision and computes only
// usage above finalized minute coverage. PostgreSQL expands the private
// coverage JSONB internally so callers never need every historical minute.
func (s *PgStore) PlanPlatformTenantStatement(ctx context.Context, accountID, tenantID string, start, end time.Time) (PlatformTenantStatementPlan, error) {
	if !end.After(start) {
		return PlatformTenantStatementPlan{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlatformTenantStatementPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from platform_tenants where account_id = $1::uuid and id = $2::uuid)`, accountID, tenantID).Scan(&exists); err != nil {
		return PlatformTenantStatementPlan{}, err
	}
	if !exists {
		return PlatformTenantStatementPlan{}, ErrNotFound
	}
	plan := PlatformTenantStatementPlan{}
	latest, err := scanPlatformTenantStatementHeader(tx.QueryRow(ctx, `select `+platformTenantStatementHeaderCols+` from platform_tenant_statements
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
		order by revision desc limit 1`, accountID, tenantID, start.UTC(), end.UTC()))
	if err == nil {
		plan.Latest, plan.HasLatest = latest, true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatementPlan{}, err
	}
	rows, err := tx.Query(ctx, `with finalized as (
		select case when jsonb_array_length(p.coverage) = 0 then p.lines else p.coverage end as coverage
		from platform_tenant_statements p
		where p.account_id = $1::uuid and p.platform_tenant_id = $2::uuid
		  and p.period_start = $3 and p.period_end = $4 and p.status = 'finalized'
	), coverage_rows as (
		select c.app_id,
			case when c.consumer_id is not null then 'consumer'
			     when c.surface_id is not null then 'surface' else 'jwt' end as source_kind,
			coalesce(c.consumer_id::text, c.surface_id::text, c.jwt_authorization_rule_id::text) as source_key,
			c.window_start, c.billable_units
		from finalized f
		cross join lateral jsonb_to_recordset(f.coverage) as c(
			app_id uuid, consumer_id uuid, surface_id uuid, jwt_authorization_rule_id uuid,
			window_start timestamptz, billable_units bigint)
	), prior_coverage as (
		select app_id, source_kind, source_key, window_start, sum(billable_units)::bigint as billable_units
		from coverage_rows group by app_id, source_kind, source_key, window_start
	), current_usage as (
		select app_id, source_kind, consumer_key as source_key, window_start,
			request_count, error_count, billable_units
		from platform_tenant_usage_minutes
		where account_id = $1::uuid and platform_tenant_id = $2::uuid
		  and window_start >= $3 and window_start < $4
	), regressions as (
		select exists(
			select 1 from prior_coverage p left join current_usage u
			using (app_id, source_kind, source_key, window_start)
			where u.app_id is null or u.billable_units < p.billable_units
		) as regressed
	), deltas as (
		select u.app_id, u.source_kind, u.source_key, u.window_start,
			u.request_count, u.error_count, u.billable_units - coalesce(p.billable_units, 0) as billable_units
		from current_usage u left join prior_coverage p
		using (app_id, source_kind, source_key, window_start)
		where u.billable_units > coalesce(p.billable_units, 0)
	)
	select r.regressed, coalesce(d.app_id::text, ''), coalesce(d.source_kind, ''), coalesce(d.source_key, ''),
		coalesce(d.window_start, $3::timestamptz), coalesce(d.request_count, 0), coalesce(d.error_count, 0), coalesce(d.billable_units, 0)
	from regressions r left join deltas d on true
	order by d.app_id, d.source_kind, d.source_key, d.window_start`, accountID, tenantID, start.UTC(), end.UTC())
	if err != nil {
		return PlatformTenantStatementPlan{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var regressed bool
		var appID, sourceKind, sourceKey string
		var windowStart time.Time
		var requestCount, errorCount, billableUnits int64
		if err := rows.Scan(&regressed, &appID, &sourceKind, &sourceKey, &windowStart, &requestCount, &errorCount, &billableUnits); err != nil {
			return PlatformTenantStatementPlan{}, err
		}
		if regressed {
			return PlatformTenantStatementPlan{}, ErrPlatformTenantUsageRegressed
		}
		if appID == "" {
			continue
		}
		bucket := APIConsumerUsageBucket{AccountID: accountID, AppID: appID, WindowStart: windowStart.UTC(),
			RequestCount: requestCount, ErrorCount: errorCount, BillableUnits: billableUnits}
		switch sourceKind {
		case "consumer":
			bucket.ConsumerKey = sourceKey
		case "surface":
			bucket.SurfaceID = sourceKey
		case "jwt":
			bucket.JWTAuthorizationRuleID = sourceKey
		default:
			return PlatformTenantStatementPlan{}, ErrInvalidArgument
		}
		plan.UsageDelta = append(plan.UsageDelta, bucket)
	}
	if err := rows.Err(); err != nil {
		return PlatformTenantStatementPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantStatementPlan{}, err
	}
	return plan, nil
}

func scanPlatformTenantStatementSummary(row pgx.Row) (PlatformTenantStatementSummary, error) {
	var out PlatformTenantStatementSummary
	var status string
	err := row.Scan(&out.ID, &out.AccountID, &out.TenantID, &out.PeriodStart, &out.PeriodEnd,
		&out.Revision, &status, &out.Currency, &out.BillableUnits, &out.UnpricedUnits,
		&out.AmountMillicents, &out.AsOf, &out.CreatedAt, &out.FinalizedAt)
	if err != nil {
		return out, err
	}
	out.Status = APIConsumerUsageStatementStatus(status)
	out.PeriodStart, out.PeriodEnd, out.AsOf = out.PeriodStart.UTC(), out.PeriodEnd.UTC(), out.AsOf.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	if out.FinalizedAt != nil {
		at := out.FinalizedAt.UTC()
		out.FinalizedAt = &at
	}
	return out, nil
}

func scanPlatformTenantHandoff(row pgx.Row) (PlatformTenantStatementHandoff, error) {
	var out PlatformTenantStatementHandoff
	err := row.Scan(&out.ID, &out.AccountID, &out.TenantID, &out.StatementID,
		&out.ExternalInvoiceID, &out.Currency, &out.AmountMillicents, &out.CreatedAt)
	out.CreatedAt = out.CreatedAt.UTC()
	return out, err
}

func (s *PgStore) ListPlatformTenantUsageMinutes(ctx context.Context, accountID, tenantID string, start, end time.Time) ([]APIConsumerUsageBucket, error) {
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	if _, err := s.GetPlatformTenant(ctx, accountID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select account_id, app_id,
		case when source_kind = 'consumer' then consumer_key else '' end,
		case when source_kind = 'surface' then consumer_key else '' end,
		case when source_kind = 'jwt' then consumer_key else '' end, window_start,
		request_count, error_count, billable_units from platform_tenant_usage_minutes
		where account_id = $1::uuid and platform_tenant_id = $2::uuid
		and window_start >= $3 and window_start < $4
		order by app_id, source_kind, consumer_key, window_start`, accountID, tenantID, start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerUsageBucket{}
	for rows.Next() {
		var b APIConsumerUsageBucket
		if err := rows.Scan(&b.AccountID, &b.AppID, &b.ConsumerKey, &b.SurfaceID, &b.JWTAuthorizationRuleID, &b.WindowStart,
			&b.RequestCount, &b.ErrorCount, &b.BillableUnits); err != nil {
			return nil, err
		}
		b.WindowStart = b.WindowStart.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PgStore) CreatePlatformTenantStatement(ctx context.Context, in PlatformTenantStatementInput) (PlatformTenantStatement, bool, error) {
	in = normalizePlatformTenantStatementInput(in)
	if err := validatePlatformTenantStatementInput(in); err != nil {
		return PlatformTenantStatement{}, false, err
	}
	lines, err := json.Marshal(in.Lines)
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	coverage, err := json.Marshal(in.Coverage)
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, in.AccountID).Scan(&locked); err != nil {
		return PlatformTenantStatement{}, false, err
	}
	var latestRevision int
	var latestStatus string
	err = tx.QueryRow(ctx, `select revision, status from platform_tenant_statements
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
		order by revision desc limit 1`, in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd).Scan(&latestRevision, &latestStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatement{}, false, err
	}
	if in.Revision <= latestRevision {
		out, err := scanPlatformTenantStatementHeader(tx.QueryRow(ctx, `select `+platformTenantStatementHeaderCols+` from platform_tenant_statements
			where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4 and revision = $5`,
			in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd, in.Revision))
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformTenantStatement{}, false, ErrConflict
		}
		return out, false, err
	}
	if in.Revision != latestRevision+1 || latestStatus != string(in.PriorStatus) {
		return PlatformTenantStatement{}, false, ErrConflict
	}
	if latestStatus == string(APIConsumerUsageStatementDraft) {
		var sameSnapshot bool
		if err := tx.QueryRow(ctx, `select currency = $6 and billable_units = $7 and unpriced_units = $8
			and amount_millicents = $9 and lines = $10::jsonb and coverage = $11::jsonb
			from platform_tenant_statements
			where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
			and revision = $5 and status = 'draft'`, in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd,
			latestRevision, in.Currency, in.BillableUnits, in.UnpricedUnits, in.AmountMillicents, lines, coverage).Scan(&sameSnapshot); err != nil {
			return PlatformTenantStatement{}, false, err
		}
		if sameSnapshot {
			out, err := scanPlatformTenantStatementHeader(tx.QueryRow(ctx, `select `+platformTenantStatementHeaderCols+` from platform_tenant_statements
				where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4 and revision = $5`,
				in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd, latestRevision))
			if err != nil {
				return PlatformTenantStatement{}, false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return PlatformTenantStatement{}, false, err
			}
			return out, false, nil
		}
		command, err := tx.Exec(ctx, `update platform_tenant_statements set status = 'superseded'
			where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
			and revision = $5 and status = 'draft'`, in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd, latestRevision)
		if err != nil {
			return PlatformTenantStatement{}, false, err
		}
		if command.RowsAffected() != 1 {
			return PlatformTenantStatement{}, false, ErrConflict
		}
	}
	out, err := scanPlatformTenantStatementHeader(tx.QueryRow(ctx, `insert into platform_tenant_statements
		(account_id, platform_tenant_id, period_start, period_end, revision, status,
		 currency, billable_units, unpriced_units, amount_millicents, lines, coverage, as_of)
		values ($1::uuid, $2::uuid, $3, $4, $5, 'draft', $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12)
		returning `+platformTenantStatementHeaderCols,
		in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd, in.Revision,
		in.Currency, in.BillableUnits, in.UnpricedUnits, in.AmountMillicents, lines, coverage, in.AsOf))
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	seen := map[string]bool{}
	for _, line := range in.Lines {
		key := line.AppID + "\x00" + line.ConsumerID + "\x00" + line.SurfaceID + "\x00" + line.JWTAuthorizationRuleID
		if seen[key] {
			continue
		}
		seen[key] = true
		if line.ConsumerID != "" {
			_, err = tx.Exec(ctx, `insert into platform_tenant_statement_consumers (statement_id, app_id, consumer_id)
				values ($1::uuid, $2::uuid, $3::uuid)`, out.ID, line.AppID, line.ConsumerID)
		} else if line.SurfaceID != "" {
			_, err = tx.Exec(ctx, `insert into platform_tenant_statement_surfaces (statement_id, app_id, surface_id)
				values ($1::uuid, $2::uuid, $3::uuid)`, out.ID, line.AppID, line.SurfaceID)
		} else {
			_, err = tx.Exec(ctx, `insert into platform_tenant_statement_jwt_rules (statement_id, app_id, authorization_rule_id)
				values ($1::uuid, $2::uuid, $3::uuid)`, out.ID, line.AppID, line.JWTAuthorizationRuleID)
		}
		if err != nil {
			return PlatformTenantStatement{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantStatement{}, false, err
	}
	return out, true, nil
}

func (s *PgStore) GetPlatformTenantStatement(ctx context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, error) {
	out, err := scanPlatformTenantStatement(s.pool.QueryRow(ctx, `select `+platformTenantStatementCols+` from platform_tenant_statements
		where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid`, statementID, accountID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatement{}, ErrNotFound
	}
	return out, err
}

func (s *PgStore) GetPlatformTenantStatementHeader(ctx context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, error) {
	out, err := scanPlatformTenantStatementHeader(s.pool.QueryRow(ctx, `select `+platformTenantStatementHeaderCols+` from platform_tenant_statements
		where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid`, statementID, accountID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatement{}, ErrNotFound
	}
	return out, err
}

func (s *PgStore) ListPlatformTenantStatements(ctx context.Context, accountID, tenantID string, start, end time.Time) ([]PlatformTenantStatement, error) {
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `select `+platformTenantStatementCols+` from platform_tenant_statements
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
		order by revision asc`, accountID, tenantID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformTenantStatement{}
	for rows.Next() {
		statement, err := scanPlatformTenantStatement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}

func (s *PgStore) ListPlatformTenantStatementHeaders(ctx context.Context, accountID, tenantID string, start, end time.Time) ([]PlatformTenantStatement, error) {
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `select `+platformTenantStatementHeaderCols+` from platform_tenant_statements
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and period_start = $3 and period_end = $4
		order by revision asc`, accountID, tenantID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformTenantStatement{}
	for rows.Next() {
		statement, err := scanPlatformTenantStatementHeader(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}

func (s *PgStore) ListFinalizedPlatformTenantStatements(ctx context.Context, accountID, tenantID string, start, end time.Time, limit, offset int) ([]PlatformTenantStatementSummary, error) {
	if !end.After(start) || limit < 1 || limit > 101 || offset < 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `select `+platformTenantStatementSummaryCols+` from platform_tenant_statements
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and status = 'finalized'
		and period_start < $4 and period_end > $3
		order by period_start desc, revision desc, created_at desc, id desc
		limit $5 offset $6`, accountID, tenantID, start, end, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PlatformTenantStatementSummary, 0, limit)
	for rows.Next() {
		statement, err := scanPlatformTenantStatementSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}

func (s *PgStore) FinalizePlatformTenantStatement(ctx context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, bool, error) {
	out, err := scanPlatformTenantStatementHeader(s.pool.QueryRow(ctx, `update platform_tenant_statements
		set status = 'finalized', finalized_at = now()
		where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid
		and status = 'draft' and unpriced_units = 0 and currency <> ''
		returning `+platformTenantStatementHeaderCols, statementID, accountID, tenantID))
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatement{}, false, err
	}
	out, err = s.GetPlatformTenantStatementHeader(ctx, accountID, tenantID, statementID)
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	if out.Status == APIConsumerUsageStatementFinalized {
		return out, false, nil
	}
	return PlatformTenantStatement{}, false, ErrConflict
}

func (s *PgStore) CreatePlatformTenantStatementHandoff(ctx context.Context, in PlatformTenantStatementHandoffInput) (PlatformTenantStatementHandoff, bool, error) {
	if err := validatePlatformTenantHandoffInput(in); err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, in.AccountID).Scan(&locked); err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	statement, err := scanPlatformTenantStatement(tx.QueryRow(ctx, `select `+platformTenantStatementCols+` from platform_tenant_statements
		where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid`, in.StatementID, in.AccountID, in.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatementHandoff{}, false, ErrNotFound
	}
	if err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	existing, err := scanPlatformTenantHandoff(tx.QueryRow(ctx, `select `+platformTenantHandoffCols+` from platform_tenant_statement_handoffs
		where statement_id = $1::uuid`, in.StatementID))
	if err == nil {
		if existing.ExternalInvoiceID != in.ExternalInvoiceID {
			return PlatformTenantStatementHandoff{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatementHandoff{}, false, err
	}
	if statement.Status != APIConsumerUsageStatementFinalized || statement.Currency == "" {
		return PlatformTenantStatementHandoff{}, false, ErrConflict
	}
	var conflict bool
	err = tx.QueryRow(ctx, `select
		exists (select 1 from api_consumer_usage_statement_handoffs h
		  join api_consumer_usage_statements a on a.id = h.statement_id
		  join platform_tenant_statement_consumers c on c.app_id = a.app_id and c.consumer_id = a.consumer_id
		 where c.statement_id = $1::uuid and a.period_start < $3 and a.period_end > $2)
		or exists (select 1 from platform_tenant_statement_handoffs h
		  join platform_tenant_statements p on p.id = h.statement_id
		  join platform_tenant_statement_consumers mine on mine.statement_id = $1::uuid
		  join platform_tenant_statement_consumers theirs on theirs.statement_id = p.id
		    and theirs.app_id = mine.app_id and theirs.consumer_id = mine.consumer_id
		 where p.period_start < $3 and p.period_end > $2
		   and not (p.platform_tenant_id = $4::uuid and p.period_start = $2 and p.period_end = $3))
		or exists (select 1 from platform_tenant_statement_handoffs h
		  join platform_tenant_statements p on p.id = h.statement_id
		  join platform_tenant_statement_surfaces mine on mine.statement_id = $1::uuid
		  join platform_tenant_statement_surfaces theirs on theirs.statement_id = p.id
		    and theirs.app_id = mine.app_id and theirs.surface_id = mine.surface_id
		 where p.period_start < $3 and p.period_end > $2
		   and not (p.platform_tenant_id = $4::uuid and p.period_start = $2 and p.period_end = $3))
		or exists (select 1 from platform_tenant_statement_handoffs h
		  join platform_tenant_statements p on p.id = h.statement_id
		  join platform_tenant_statement_jwt_rules mine on mine.statement_id = $1::uuid
		  join platform_tenant_statement_jwt_rules theirs on theirs.statement_id = p.id
		    and theirs.app_id = mine.app_id and theirs.authorization_rule_id = mine.authorization_rule_id
		 where p.period_start < $3 and p.period_end > $2
		   and not (p.platform_tenant_id = $4::uuid and p.period_start = $2 and p.period_end = $3))
		or exists (select 1 from api_consumer_usage_statement_handoffs where account_id = $5::uuid and external_invoice_id = $6)
		or exists (select 1 from platform_tenant_statement_handoffs where account_id = $5::uuid and external_invoice_id = $6)`,
		in.StatementID, statement.PeriodStart, statement.PeriodEnd, in.TenantID, in.AccountID, in.ExternalInvoiceID).Scan(&conflict)
	if err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	if conflict {
		return PlatformTenantStatementHandoff{}, false, ErrConflict
	}
	out, err := scanPlatformTenantHandoff(tx.QueryRow(ctx, `insert into platform_tenant_statement_handoffs
		(account_id, platform_tenant_id, statement_id, external_invoice_id, currency, amount_millicents)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6) returning `+platformTenantHandoffCols,
		in.AccountID, in.TenantID, in.StatementID, in.ExternalInvoiceID, statement.Currency, statement.AmountMillicents))
	if err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	return out, true, nil
}

func (s *PgStore) GetPlatformTenantStatementHandoff(ctx context.Context, accountID, tenantID, statementID string) (PlatformTenantStatementHandoff, error) {
	out, err := scanPlatformTenantHandoff(s.pool.QueryRow(ctx, `select `+platformTenantHandoffCols+` from platform_tenant_statement_handoffs
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and statement_id = $3::uuid`, accountID, tenantID, statementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatementHandoff{}, ErrNotFound
	}
	return out, err
}
