package state

import (
	"context"
	"errors"

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

	row := tx.QueryRow(ctx,
		`insert into api_consumer_usage_statement_handoffs
		       (account_id, app_id, consumer_id, statement_id,
		        external_invoice_id, currency, amount_millicents)
		 select statement.account_id, statement.app_id, statement.consumer_id,
		        statement.id, $5, statement.currency, statement.amount_millicents
		   from api_consumer_usage_statements statement
		  where statement.id = $4::uuid and statement.account_id = $1::uuid
		    and statement.app_id = $2::uuid and statement.consumer_id = $3::uuid
		    and statement.status = 'finalized'
		 on conflict do nothing
		 returning `+apiConsumerUsageStatementHandoffSelectCols,
		input.AccountID, input.AppID, input.ConsumerID, input.StatementID, input.ExternalInvoiceID)
	handoff, err := scanAPIConsumerUsageStatementHandoffRow(row)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return APIConsumerUsageStatementHandoff{}, false, err
		}
		return handoff, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, err
	}

	row = tx.QueryRow(ctx,
		`select `+apiConsumerUsageStatementHandoffSelectCols+`
		   from api_consumer_usage_statement_handoffs
		  where account_id = $1::uuid and app_id = $2::uuid
		    and consumer_id = $3::uuid and statement_id = $4::uuid`,
		input.AccountID, input.AppID, input.ConsumerID, input.StatementID)
	handoff, err = scanAPIConsumerUsageStatementHandoffRow(row)
	if err == nil {
		if handoff.ExternalInvoiceID != input.ExternalInvoiceID {
			return APIConsumerUsageStatementHandoff{}, false, ErrConflict
		}
		return handoff, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, err
	}

	var status string
	err = tx.QueryRow(ctx,
		`select status from api_consumer_usage_statements
		  where id = $1::uuid and account_id = $2::uuid
		    and app_id = $3::uuid and consumer_id = $4::uuid`,
		input.StatementID, input.AccountID, input.AppID, input.ConsumerID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerUsageStatementHandoff{}, false, ErrNotFound
	}
	if err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	// A draft statement, or a unique external invoice reference already used
	// by another statement, is a conflict. The handler exposes one stable
	// 409 contract for both cases and never creates a partial claim.
	if status != string(APIConsumerUsageStatementFinalized) {
		return APIConsumerUsageStatementHandoff{}, false, ErrConflict
	}
	return APIConsumerUsageStatementHandoff{}, false, ErrConflict
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
