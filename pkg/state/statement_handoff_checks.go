package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// StatementHandoffChecker reports whether ADR-238's overlap rules would
// reject a handoff of a statement, so an export or billing run can stop
// before it creates an invoice (ADR-1503). Invoice-ID reuse is not checked:
// the export chooses its own invoice ID.
type StatementHandoffChecker interface {
	StatementHandoffBlocked(ctx context.Context, accountID, sourceKind, statementID string) (bool, error)
}

// apiConsumerHandoffOverlapSQL is true when a handoff of another statement
// already covers part of an app consumer statement's usage. Parameters: $1
// app, $2 consumer, $3 period start, $4 period end.
const apiConsumerHandoffOverlapSQL = `exists (select 1 from platform_tenant_statement_handoffs h
		  join platform_tenant_statements p on p.id = h.statement_id
		  join platform_tenant_statement_consumers c on c.statement_id = p.id
		 where c.app_id = $1::uuid and c.consumer_id = $2::uuid
		   and p.period_start < $4 and p.period_end > $3)
		or exists (select 1 from api_consumer_usage_statement_handoffs h
		  join api_consumer_usage_statements a on a.id = h.statement_id
		 where a.app_id = $1::uuid and a.consumer_id = $2::uuid
		   and a.period_start < $4 and a.period_end > $3
		   -- Revisions of the same period are additive adjustments that
		   -- never repeat finalized units, so each may be handed off.
		   and not (a.period_start = $3 and a.period_end = $4))`

// tenantHandoffOverlapSQL is true when a handoff of another statement
// already covers a consumer, surface or JWT rule of a tenant statement in an
// overlapping window. Parameters: $1 statement, $2 period start, $3 period
// end, $4 tenant.
const tenantHandoffOverlapSQL = `exists (select 1 from api_consumer_usage_statement_handoffs h
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
		   and not (p.platform_tenant_id = $4::uuid and p.period_start = $2 and p.period_end = $3))`

func (s *PgStore) StatementHandoffBlocked(ctx context.Context, accountID, sourceKind, statementID string) (bool, error) {
	var start, end time.Time
	var blocked bool
	switch sourceKind {
	case StatementSourceAppConsumer:
		var appID, consumerID string
		err := s.pool.QueryRow(ctx, `select app_id::text, consumer_id::text, period_start, period_end
			from api_consumer_usage_statements where id = $1::uuid and account_id = $2::uuid`, statementID, accountID).
			Scan(&appID, &consumerID, &start, &end)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		if err != nil {
			return false, fmt.Errorf("load app consumer statement: %w", err)
		}
		err = s.pool.QueryRow(ctx, `select `+apiConsumerHandoffOverlapSQL, appID, consumerID, start, end).Scan(&blocked)
		if err != nil {
			return false, fmt.Errorf("check app consumer handoff overlap: %w", err)
		}
	case StatementSourcePlatformTenant:
		var tenantID string
		err := s.pool.QueryRow(ctx, `select platform_tenant_id::text, period_start, period_end
			from platform_tenant_statements where id = $1::uuid and account_id = $2::uuid`, statementID, accountID).
			Scan(&tenantID, &start, &end)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		if err != nil {
			return false, fmt.Errorf("load platform tenant statement: %w", err)
		}
		err = s.pool.QueryRow(ctx, `select `+tenantHandoffOverlapSQL, statementID, start, end, tenantID).Scan(&blocked)
		if err != nil {
			return false, fmt.Errorf("check platform tenant handoff overlap: %w", err)
		}
	case StatementSourceProductControl:
		// A Product Control statement invoices only its own app, customer and
		// month of receipts; no other statement covers them.
		var exists bool
		err := s.pool.QueryRow(ctx, `select exists (select 1 from product_control_billing_documents
			where id = $1::uuid and account_id = $2::uuid and kind = 'statement')`, statementID, accountID).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("load product control statement: %w", err)
		}
		if !exists {
			return false, ErrNotFound
		}
	default:
		return false, ErrNotFound
	}
	return blocked, nil
}

func (m *MemStore) StatementHandoffBlocked(_ context.Context, accountID, sourceKind, statementID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch sourceKind {
	case StatementSourceAppConsumer:
		statement, ok := m.apiConsumerUsageStatements[statementID]
		if !ok || statement.AccountID != accountID {
			return false, ErrNotFound
		}
		return m.apiConsumerHandoffOverlapLocked(statement), nil
	case StatementSourcePlatformTenant:
		statement, ok := m.platformTenantStatements[statementID]
		if !ok || statement.AccountID != accountID {
			return false, ErrNotFound
		}
		return m.tenantHandoffOverlapLocked(statement), nil
	case StatementSourceProductControl:
		row, ok := m.productLocked().documents[statementID]
		if !ok || row.account != accountID || row.document.Kind != "statement" {
			return false, ErrNotFound
		}
		return false, nil
	}
	return false, ErrNotFound
}

// externalInvoiceHandedOffLocked reports whether the account already used
// invoiceID for any statement handoff.
func (m *MemStore) externalInvoiceHandedOffLocked(accountID, invoiceID string) bool {
	for _, h := range m.apiConsumerUsageStatementHandoffs {
		if h.AccountID == accountID && h.ExternalInvoiceID == invoiceID {
			return true
		}
	}
	for _, h := range m.platformTenantStatementHandoffs {
		if h.AccountID == accountID && h.ExternalInvoiceID == invoiceID {
			return true
		}
	}
	return false
}

// apiConsumerHandoffOverlapLocked mirrors apiConsumerHandoffOverlapSQL.
func (m *MemStore) apiConsumerHandoffOverlapLocked(statement APIConsumerUsageStatement) bool {
	for _, h := range m.apiConsumerUsageStatementHandoffs {
		other := m.apiConsumerUsageStatements[h.StatementID]
		samePeriod := other.PeriodStart.Equal(statement.PeriodStart) && other.PeriodEnd.Equal(statement.PeriodEnd)
		if other.AppID == statement.AppID && other.ConsumerID == statement.ConsumerID && !samePeriod &&
			windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) {
			return true
		}
	}
	for _, h := range m.platformTenantStatementHandoffs {
		other := m.platformTenantStatements[h.StatementID]
		if h.AccountID == statement.AccountID &&
			windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) &&
			tenantStatementIncludesConsumer(other, statement.AppID, statement.ConsumerID) {
			return true
		}
	}
	return false
}

// tenantHandoffOverlapLocked mirrors tenantHandoffOverlapSQL.
func (m *MemStore) tenantHandoffOverlapLocked(statement PlatformTenantStatement) bool {
	for _, h := range m.apiConsumerUsageStatementHandoffs {
		other := m.apiConsumerUsageStatements[h.StatementID]
		if h.AccountID == statement.AccountID &&
			windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) &&
			tenantStatementIncludesConsumer(statement, other.AppID, other.ConsumerID) {
			return true
		}
	}
	for _, h := range m.platformTenantStatementHandoffs {
		other := m.platformTenantStatements[h.StatementID]
		if h.AccountID != statement.AccountID ||
			(other.TenantID == statement.TenantID && other.PeriodStart.Equal(statement.PeriodStart) && other.PeriodEnd.Equal(statement.PeriodEnd)) ||
			!windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) {
			continue
		}
		for _, line := range statement.Lines {
			if tenantStatementIncludesConsumer(other, line.AppID, line.ConsumerID) ||
				tenantStatementIncludesSurface(other, line.AppID, line.SurfaceID) ||
				tenantStatementIncludesJWTAuthorizationRule(other, line.AppID, line.JWTAuthorizationRuleID) {
				return true
			}
		}
	}
	return false
}
