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
       lines, as_of, created_at, finalized_at`
const platformTenantHandoffCols = `id, account_id, platform_tenant_id, statement_id,
       external_invoice_id, currency, amount_millicents, created_at`

func scanPlatformTenantStatement(row pgx.Row) (PlatformTenantStatement, error) {
	var out PlatformTenantStatement
	var status string
	var raw []byte
	err := row.Scan(&out.ID, &out.AccountID, &out.TenantID, &out.PeriodStart, &out.PeriodEnd,
		&out.Revision, &status, &out.Currency, &out.BillableUnits, &out.UnpricedUnits,
		&out.AmountMillicents, &raw, &out.AsOf, &out.CreatedAt, &out.FinalizedAt)
	if err != nil {
		return out, err
	}
	out.Status = APIConsumerUsageStatementStatus(status)
	if err := json.Unmarshal(raw, &out.Lines); err != nil {
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
		case when source_kind = 'surface' then consumer_key else '' end, window_start,
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
		if err := rows.Scan(&b.AccountID, &b.AppID, &b.ConsumerKey, &b.SurfaceID, &b.WindowStart,
			&b.RequestCount, &b.ErrorCount, &b.BillableUnits); err != nil {
			return nil, err
		}
		b.WindowStart = b.WindowStart.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PgStore) CreatePlatformTenantStatement(ctx context.Context, in PlatformTenantStatementInput) (PlatformTenantStatement, bool, error) {
	if err := validatePlatformTenantStatementInput(in); err != nil {
		return PlatformTenantStatement{}, false, err
	}
	lines, err := json.Marshal(in.Lines)
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
		out, err := scanPlatformTenantStatement(tx.QueryRow(ctx, `select `+platformTenantStatementCols+` from platform_tenant_statements
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
	out, err := scanPlatformTenantStatement(tx.QueryRow(ctx, `insert into platform_tenant_statements
		(account_id, platform_tenant_id, period_start, period_end, revision, status,
		 currency, billable_units, unpriced_units, amount_millicents, lines, as_of)
		values ($1::uuid, $2::uuid, $3, $4, $5, 'draft', $6, $7, $8, $9, $10::jsonb, $11)
		returning `+platformTenantStatementCols,
		in.AccountID, in.TenantID, in.PeriodStart, in.PeriodEnd, in.Revision,
		in.Currency, in.BillableUnits, in.UnpricedUnits, in.AmountMillicents, lines, in.AsOf))
	if err != nil {
		return PlatformTenantStatement{}, false, err
	}
	seen := map[string]bool{}
	for _, line := range in.Lines {
		key := line.AppID + "\x00" + line.ConsumerID + "\x00" + line.SurfaceID
		if seen[key] {
			continue
		}
		seen[key] = true
		if line.ConsumerID != "" {
			_, err = tx.Exec(ctx, `insert into platform_tenant_statement_consumers (statement_id, app_id, consumer_id)
				values ($1::uuid, $2::uuid, $3::uuid)`, out.ID, line.AppID, line.ConsumerID)
		} else {
			_, err = tx.Exec(ctx, `insert into platform_tenant_statement_surfaces (statement_id, app_id, surface_id)
				values ($1::uuid, $2::uuid, $3::uuid)`, out.ID, line.AppID, line.SurfaceID)
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

func (s *PgStore) FinalizePlatformTenantStatement(ctx context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, bool, error) {
	out, err := scanPlatformTenantStatement(s.pool.QueryRow(ctx, `update platform_tenant_statements
		set status = 'finalized', finalized_at = now()
		where id = $1::uuid and account_id = $2::uuid and platform_tenant_id = $3::uuid
		and status = 'draft' and unpriced_units = 0 and currency <> ''
		returning `+platformTenantStatementCols, statementID, accountID, tenantID))
	if err == nil {
		return out, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantStatement{}, false, err
	}
	out, err = s.GetPlatformTenantStatement(ctx, accountID, tenantID, statementID)
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
