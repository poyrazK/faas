package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const apiConsumerUsageStatementSelectCols = `id, account_id, app_id, consumer_id,
       period_start, period_end, revision, status, currency, billable_units,
       unpriced_units, amount_millicents, priced, buckets, as_of,
       created_at, finalized_at`

type apiConsumerUsageStatementRowScanner interface{ Scan(dest ...any) error }

func scanAPIConsumerUsageStatementRow(row apiConsumerUsageStatementRowScanner) (APIConsumerUsageStatement, error) {
	var statement APIConsumerUsageStatement
	var status string
	var buckets []byte
	if err := row.Scan(
		&statement.ID, &statement.AccountID, &statement.AppID, &statement.ConsumerID,
		&statement.PeriodStart, &statement.PeriodEnd, &statement.Revision, &status, &statement.Currency,
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
	// Serialize revisions of this consumer's statements so two planners cannot
	// both supersede the same draft or claim the same revision number.
	var locked string
	if err := tx.QueryRow(ctx, `select id from api_consumers
		where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid for update`,
		input.ConsumerID, input.AccountID, input.AppID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumerUsageStatement{}, false, ErrNotFound
		}
		return APIConsumerUsageStatement{}, false, err
	}
	periodKey := []any{input.AccountID, input.AppID, input.ConsumerID, input.PeriodStart, input.PeriodEnd}
	latest, err := scanAPIConsumerUsageStatementRow(tx.QueryRow(ctx, `select `+apiConsumerUsageStatementSelectCols+`
		from api_consumer_usage_statements
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		  and period_start = $4 and period_end = $5
		order by revision desc limit 1`, periodKey...))
	hasLatest := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatement{}, false, err
	}
	if hasLatest && input.Revision <= latest.Revision {
		// A retry of an already-persisted plan replays that exact revision.
		existing, err := scanAPIConsumerUsageStatementRow(tx.QueryRow(ctx, `select `+apiConsumerUsageStatementSelectCols+`
			from api_consumer_usage_statements
			where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
			  and period_start = $4 and period_end = $5 and revision = $6`, append(periodKey, input.Revision)...))
		if errors.Is(err, pgx.ErrNoRows) {
			return APIConsumerUsageStatement{}, false, ErrConflict
		}
		return existing, false, err
	}
	if !hasLatest && input.Revision != 1 {
		return APIConsumerUsageStatement{}, false, ErrConflict
	}
	if hasLatest && (input.Revision != latest.Revision+1 || latest.Status != input.PriorStatus) {
		return APIConsumerUsageStatement{}, false, ErrConflict
	}
	if hasLatest && latest.Status == APIConsumerUsageStatementDraft {
		if sameAPIConsumerUsageStatementSnapshot(latest, input) {
			if err := tx.Commit(ctx); err != nil {
				return APIConsumerUsageStatement{}, false, err
			}
			return latest, false, nil
		}
		command, err := tx.Exec(ctx, `update api_consumer_usage_statements set status = 'superseded'
			where id = $1::uuid and status = 'draft'`, latest.ID)
		if err != nil {
			return APIConsumerUsageStatement{}, false, err
		}
		if command.RowsAffected() != 1 {
			return APIConsumerUsageStatement{}, false, ErrConflict
		}
	}
	statement, err := scanAPIConsumerUsageStatementRow(tx.QueryRow(ctx,
		`insert into api_consumer_usage_statements
		       (account_id, app_id, consumer_id, period_start, period_end, revision, status,
		        currency, billable_units, unpriced_units, amount_millicents,
		        priced, buckets, as_of)
		 values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 'draft', $7,
		         $8, $9, $10, $11, $12::jsonb, $13)
		 returning `+apiConsumerUsageStatementSelectCols,
		input.AccountID, input.AppID, input.ConsumerID, input.PeriodStart, input.PeriodEnd, input.Revision,
		input.Currency, input.BillableUnits, input.UnpricedUnits, input.AmountMillicents,
		input.Priced, buckets, input.AsOf))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return APIConsumerUsageStatement{}, false, ErrConflict
		}
		return APIConsumerUsageStatement{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	return statement, true, nil
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
		  order by period_start desc, revision desc, created_at desc, id desc`, accountID, appID, consumerID)
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

func (s *PgStore) ListAPIConsumerUsageStatementRevisions(ctx context.Context, accountID, appID, consumerID string, start, end time.Time) ([]APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || consumerID == "" {
		return nil, ErrNotFound
	}
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx,
		`select `+apiConsumerUsageStatementSelectCols+`
		   from api_consumer_usage_statements
		  where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		    and period_start = $4 and period_end = $5
		  order by revision asc`, accountID, appID, consumerID, start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIConsumerUsageStatement{}
	for rows.Next() {
		statement, err := scanAPIConsumerUsageStatementRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}

func (s *PgStore) FinalizeAPIConsumerUsageStatement(ctx context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatement, bool, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatement{}, false, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerUsageStatement{}, false, fmt.Errorf("state: begin usage statement finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx,
		`update api_consumer_usage_statements
		    set status = 'finalized', finalized_at = now()
		  where id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid
		    and consumer_id = $4::uuid and status = 'draft' and unpriced_units = 0
		 returning `+apiConsumerUsageStatementSelectCols,
		statementID, accountID, appID, consumerID)
	statement, err := scanAPIConsumerUsageStatementRow(row)
	if err == nil {
		payload, payloadErr := usageStatementFinalizedWebhookPayload(statement)
		if payloadErr != nil {
			return APIConsumerUsageStatement{}, false, payloadErr
		}
		if _, insertErr := tx.Exec(ctx, `
			insert into app_webhook_event_outbox
				(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
			select $1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, array_agg(h.id order by h.id)
			  from app_webhooks h
			 where h.account_id = $1::uuid and h.app_id = $2::uuid
			   and h.scope = 'app' and h.enabled
			   and (cardinality(h.event_filter) = 0 or $3 = any(h.event_filter))
			having count(*) > 0
			on conflict (event, source_id) do nothing
		`, accountID, appID, string(AppWebhookEventUsageStatementFinalized), statement.ID, string(payload)); insertErr != nil {
			return APIConsumerUsageStatement{}, false, fmt.Errorf("state: enqueue finalized usage statement webhook event: %w", insertErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return APIConsumerUsageStatement{}, false, fmt.Errorf("state: commit usage statement finalization: %w", commitErr)
		}
		return statement, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatement{}, false, err
	}
	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
		return APIConsumerUsageStatement{}, false, fmt.Errorf("state: rollback unchanged usage statement finalization: %w", rollbackErr)
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
