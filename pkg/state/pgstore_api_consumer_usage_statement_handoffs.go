package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const apiConsumerUsageStatementHandoffSelectCols = `id, account_id, app_id,
       consumer_id, statement_id, external_invoice_id, currency,
       amount_millicents, created_at`

type apiConsumerUsageStatementHandoffRowScanner interface{ Scan(dest ...any) error }

func scanAPIConsumerUsageStatementHandoffRow(row apiConsumerUsageStatementHandoffRowScanner) (APIConsumerUsageStatementHandoff, error) {
	var handoff APIConsumerUsageStatementHandoff
	if err := row.Scan(
		&handoff.ID, &handoff.AccountID, &handoff.AppID, &handoff.ConsumerID,
		&handoff.StatementID, &handoff.ExternalInvoiceID, &handoff.Currency,
		&handoff.AmountMillicents, &handoff.CreatedAt,
	); err != nil {
		return APIConsumerUsageStatementHandoff{}, err
	}
	handoff.CreatedAt = handoff.CreatedAt.UTC()
	return handoff, nil
}

func (s *PgStore) CreateAPIConsumerUsageStatementHandoff(ctx context.Context, input APIConsumerUsageStatementHandoffInput) (APIConsumerUsageStatementHandoff, bool, error) {
	if err := validateAPIConsumerUsageStatementHandoffInput(input); err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize all external billing claims for this account, including the
	// cross-app tenant path, before checking overlapping usage windows.
	var locked string
	if err := tx.QueryRow(ctx, `select id from accounts where id = $1::uuid for update`, input.AccountID).Scan(&locked); err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	row := tx.QueryRow(ctx,
		`select `+apiConsumerUsageStatementHandoffSelectCols+`
		   from api_consumer_usage_statement_handoffs
		  where account_id = $1::uuid and app_id = $2::uuid
		    and consumer_id = $3::uuid and statement_id = $4::uuid`,
		input.AccountID, input.AppID, input.ConsumerID, input.StatementID)
	handoff, err := scanAPIConsumerUsageStatementHandoffRow(row)
	if err == nil {
		if handoff.ExternalInvoiceID != input.ExternalInvoiceID {
			return APIConsumerUsageStatementHandoff{}, false, ErrConflict
		}
		return handoff, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, err
	}

	var status, currency string
	var amount int64
	var start, end time.Time
	err = tx.QueryRow(ctx,
		`select status, coalesce(currency, ''), amount_millicents, period_start, period_end from api_consumer_usage_statements
		  where id = $1::uuid and account_id = $2::uuid
		    and app_id = $3::uuid and consumer_id = $4::uuid`,
		input.StatementID, input.AccountID, input.AppID, input.ConsumerID).Scan(&status, &currency, &amount, &start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, ErrNotFound
	}
	if err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	if status != string(APIConsumerUsageStatementFinalized) {
		return APIConsumerUsageStatementHandoff{}, false, ErrConflict
	}
	var conflict bool
	err = tx.QueryRow(ctx, `select
		exists (select 1 from platform_tenant_statement_handoffs h
		  join platform_tenant_statements p on p.id = h.statement_id
		  join platform_tenant_statement_consumers c on c.statement_id = p.id
		 where c.app_id = $1::uuid and c.consumer_id = $2::uuid
		   and p.period_start < $4 and p.period_end > $3)
		or exists (select 1 from api_consumer_usage_statement_handoffs h
		  join api_consumer_usage_statements a on a.id = h.statement_id
		 where a.app_id = $1::uuid and a.consumer_id = $2::uuid
		   and a.period_start < $4 and a.period_end > $3)
		or exists (select 1 from platform_tenant_statement_handoffs
		 where account_id = $5::uuid and external_invoice_id = $6)`,
		input.AppID, input.ConsumerID, start, end, input.AccountID, input.ExternalInvoiceID).Scan(&conflict)
	if err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	if conflict {
		return APIConsumerUsageStatementHandoff{}, false, ErrConflict
	}
	handoff, err = scanAPIConsumerUsageStatementHandoffRow(tx.QueryRow(ctx,
		`insert into api_consumer_usage_statement_handoffs
		 (account_id, app_id, consumer_id, statement_id, external_invoice_id, currency, amount_millicents)
		 values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7)
		 on conflict do nothing returning `+apiConsumerUsageStatementHandoffSelectCols,
		input.AccountID, input.AppID, input.ConsumerID, input.StatementID, input.ExternalInvoiceID, currency, amount))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, ErrConflict
	}
	if err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	return handoff, true, nil
}

func (s *PgStore) GetAPIConsumerUsageStatementHandoff(ctx context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatementHandoff, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatementHandoff{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+apiConsumerUsageStatementHandoffSelectCols+`
		   from api_consumer_usage_statement_handoffs
		  where account_id = $1::uuid and app_id = $2::uuid
		    and consumer_id = $3::uuid and statement_id = $4::uuid`,
		accountID, appID, consumerID, statementID)
	handoff, err := scanAPIConsumerUsageStatementHandoffRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, ErrNotFound
	}
	return handoff, err
}
