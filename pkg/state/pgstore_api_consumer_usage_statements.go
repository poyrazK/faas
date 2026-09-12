package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerUsageStatementSelectCols = `id, account_id, app_id, consumer_id,
       period_start, period_end, status, currency, billable_units,
       unpriced_units, amount_millicents, priced, buckets, as_of,
       created_at, finalized_at`

type apiConsumerUsageStatementRowScanner interface{ Scan(dest ...any) error }

func scanAPIConsumerUsageStatementRow(row apiConsumerUsageStatementRowScanner) (APIConsumerUsageStatement, error) {
	var statement APIConsumerUsageStatement
	var status string
	var buckets []byte
	if err := row.Scan(
		&statement.ID, &statement.AccountID, &statement.AppID, &statement.ConsumerID,
		&statement.PeriodStart, &statement.PeriodEnd, &status, &statement.Currency,
		&statement.BillableUnits, &statement.UnpricedUnits, &statement.AmountMillicents,
		&statement.Priced, &buckets, &statement.AsOf, &statement.CreatedAt, &statement.FinalizedAt,
	); err != nil {
		return APIConsumerUsageStatement{}, err
	}
	statement.Status = APIConsumerUsageStatementStatus(status)
	if len(buckets) == 0 {
		statement.Buckets = []APIConsumerUsageStatementBucket{}
	} else if err := json.Unmarshal(buckets, &statement.Buckets); err != nil {
		return APIConsumerUsageStatement{}, err
	}
	statement.PeriodStart = statement.PeriodStart.UTC()
	statement.PeriodEnd = statement.PeriodEnd.UTC()
	statement.AsOf = statement.AsOf.UTC()
	statement.CreatedAt = statement.CreatedAt.UTC()
	if statement.FinalizedAt != nil {
		finalizedAt := statement.FinalizedAt.UTC()
		statement.FinalizedAt = &finalizedAt
	}
	for i := range statement.Buckets {
		statement.Buckets[i].WindowStart = statement.Buckets[i].WindowStart.UTC()
	}
	return statement, nil
}

func (s *PgStore) CreateAPIConsumerUsageStatement(ctx context.Context, input APIConsumerUsageStatementInput) (APIConsumerUsageStatement, bool, error) {
	input.Currency = normalizeStatementCurrency(input.Currency)
	input.PeriodStart = input.PeriodStart.UTC()
	input.PeriodEnd = input.PeriodEnd.UTC()
	input.AsOf = input.AsOf.UTC()
	if err := validateAPIConsumerUsageStatementInput(input); err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	bucketValues := input.Buckets
	if bucketValues == nil {
		bucketValues = []APIConsumerUsageStatementBucket{}
	}
	buckets, err := json.Marshal(bucketValues)
	if err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx,
		`insert into api_consumer_usage_statements
		       (account_id, app_id, consumer_id, period_start, period_end, status,
		        currency, billable_units, unpriced_units, amount_millicents,
		        priced, buckets, as_of)
		 values ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'draft', $6,
		         $7, $8, $9, $10, $11::jsonb, $12)
		 on conflict (app_id, consumer_id, period_start, period_end) do nothing
		 returning `+apiConsumerUsageStatementSelectCols,
		input.AccountID, input.AppID, input.ConsumerID, input.PeriodStart, input.PeriodEnd,
		input.Currency, input.BillableUnits, input.UnpricedUnits, input.AmountMillicents,
		input.Priced, buckets, input.AsOf)
	statement, err := scanAPIConsumerUsageStatementRow(row)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return APIConsumerUsageStatement{}, false, err
		}
		return statement, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Fall through to the natural-key lookup below.
		} else {
			return APIConsumerUsageStatement{}, false, err
		}
	}
	row = tx.QueryRow(ctx,
		`select `+apiConsumerUsageStatementSelectCols+`
		   from api_consumer_usage_statements
		  where app_id = $1::uuid and consumer_id = $2::uuid
		    and period_start = $3 and period_end = $4`,
		input.AppID, input.ConsumerID, input.PeriodStart, input.PeriodEnd)
	statement, err = scanAPIConsumerUsageStatementRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumerUsageStatement{}, false, ErrNotFound
		}
		return APIConsumerUsageStatement{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	return statement, false, nil
}

func (s *PgStore) GetAPIConsumerUsageStatement(ctx context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatement{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+apiConsumerUsageStatementSelectCols+`
		   from api_consumer_usage_statements
		  where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid and consumer_id = $4::uuid`,
		statementID, accountID, appID, consumerID)
	statement, err := scanAPIConsumerUsageStatementRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatement{}, ErrNotFound
	}
	return statement, err
}

func (s *PgStore) ListAPIConsumerUsageStatements(ctx context.Context, accountID, appID, consumerID string) ([]APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || consumerID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerUsageStatementSelectCols+`
		   from api_consumer_usage_statements
		  where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		  order by period_start desc, created_at desc, id desc`, accountID, appID, consumerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerUsageStatement
	for rows.Next() {
		statement, err := scanAPIConsumerUsageStatementRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PgStore) FinalizeAPIConsumerUsageStatement(ctx context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatement, bool, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatement{}, false, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`update api_consumer_usage_statements
		    set status = 'finalized', finalized_at = now()
		  where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid
		    and consumer_id = $4::uuid and status = 'draft' and unpriced_units = 0
		 returning `+apiConsumerUsageStatementSelectCols,
		statementID, accountID, appID, consumerID)
	statement, err := scanAPIConsumerUsageStatementRow(row)
	if err == nil {
		return statement, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatement{}, false, err
	}
	current, getErr := s.GetAPIConsumerUsageStatement(ctx, accountID, appID, consumerID, statementID)
	if getErr != nil {
		return APIConsumerUsageStatement{}, false, getErr
	}
	if current.Status == APIConsumerUsageStatementFinalized {
		return current, false, nil
	}
	return APIConsumerUsageStatement{}, false, ErrConflict
}
