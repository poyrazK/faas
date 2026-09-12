package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func usageStatementNaturalKey(appID, consumerID string, start, end time.Time) string {
	return appID + "\x00" + consumerID + "\x00" + start.UTC().Format(time.RFC3339) + "\x00" + end.UTC().Format(time.RFC3339)
}

func (m *MemStore) CreateAPIConsumerUsageStatement(_ context.Context, input APIConsumerUsageStatementInput) (APIConsumerUsageStatement, bool, error) {
	input.Currency = normalizeStatementCurrency(input.Currency)
	input.PeriodStart = input.PeriodStart.UTC()
	input.PeriodEnd = input.PeriodEnd.UTC()
	input.AsOf = input.AsOf.UTC()
	if err := validateAPIConsumerUsageStatementInput(input); err != nil {
		return APIConsumerUsageStatement{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := usageStatementNaturalKey(input.AppID, input.ConsumerID, input.PeriodStart, input.PeriodEnd)
	for _, existing := range m.apiConsumerUsageStatements {
		if usageStatementNaturalKey(existing.AppID, existing.ConsumerID, existing.PeriodStart, existing.PeriodEnd) == key {
			return cloneAPIConsumerUsageStatement(existing), false, nil
		}
	}
	now := time.Now().UTC()
	statement := APIConsumerUsageStatement{
		ID: uuid.NewString(), AccountID: input.AccountID, AppID: input.AppID, ConsumerID: input.ConsumerID,
		PeriodStart: input.PeriodStart, PeriodEnd: input.PeriodEnd, Status: APIConsumerUsageStatementDraft,
		Currency: input.Currency, BillableUnits: input.BillableUnits, UnpricedUnits: input.UnpricedUnits,
		AmountMillicents: input.AmountMillicents, Priced: input.Priced,
		Buckets: append([]APIConsumerUsageStatementBucket(nil), input.Buckets...), AsOf: input.AsOf, CreatedAt: now,
	}
	m.apiConsumerUsageStatements[statement.ID] = statement
	return cloneAPIConsumerUsageStatement(statement), true, nil
}

func (m *MemStore) GetAPIConsumerUsageStatement(_ context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatement{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	statement, ok := m.apiConsumerUsageStatements[statementID]
	if !ok || statement.AccountID != accountID || statement.AppID != appID || statement.ConsumerID != consumerID {
		return APIConsumerUsageStatement{}, ErrNotFound
	}
	return cloneAPIConsumerUsageStatement(statement), nil
}

func (m *MemStore) ListAPIConsumerUsageStatements(_ context.Context, accountID, appID, consumerID string) ([]APIConsumerUsageStatement, error) {
	if accountID == "" || appID == "" || consumerID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumerUsageStatement
	for _, statement := range m.apiConsumerUsageStatements {
		if statement.AccountID == accountID && statement.AppID == appID && statement.ConsumerID == consumerID {
			out = append(out, cloneAPIConsumerUsageStatement(statement))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PeriodStart.Equal(out[j].PeriodStart) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].PeriodStart.After(out[j].PeriodStart)
	})
	return out, nil
}

func (m *MemStore) FinalizeAPIConsumerUsageStatement(_ context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatement, bool, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatement{}, false, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	statement, ok := m.apiConsumerUsageStatements[statementID]
	if !ok || statement.AccountID != accountID || statement.AppID != appID || statement.ConsumerID != consumerID {
		return APIConsumerUsageStatement{}, false, ErrNotFound
	}
	if statement.Status == APIConsumerUsageStatementFinalized {
		return cloneAPIConsumerUsageStatement(statement), false, nil
	}
	if statement.UnpricedUnits > 0 {
		return APIConsumerUsageStatement{}, false, ErrConflict
	}
	now := time.Now().UTC()
	statement.Status = APIConsumerUsageStatementFinalized
	statement.FinalizedAt = &now
	m.apiConsumerUsageStatements[statement.ID] = statement
	return cloneAPIConsumerUsageStatement(statement), true, nil
}

func (m *MemStore) CreateAPIConsumerUsageStatementHandoff(_ context.Context, input APIConsumerUsageStatementHandoffInput) (APIConsumerUsageStatementHandoff, bool, error) {
	if err := validateAPIConsumerUsageStatementHandoffInput(input); err != nil {
		return APIConsumerUsageStatementHandoff{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	statement, ok := m.apiConsumerUsageStatements[input.StatementID]
	if !ok || statement.AccountID != input.AccountID || statement.AppID != input.AppID || statement.ConsumerID != input.ConsumerID {
		return APIConsumerUsageStatementHandoff{}, false, ErrNotFound
	}
	if statement.Status != APIConsumerUsageStatementFinalized {
		return APIConsumerUsageStatementHandoff{}, false, ErrConflict
	}
	if existing, ok := m.apiConsumerUsageStatementHandoffs[input.StatementID]; ok {
		if existing.ExternalInvoiceID != input.ExternalInvoiceID {
			return APIConsumerUsageStatementHandoff{}, false, ErrConflict
		}
		return cloneAPIConsumerUsageStatementHandoff(existing), false, nil
	}
	for _, existing := range m.apiConsumerUsageStatementHandoffs {
		if existing.AccountID == input.AccountID && existing.ExternalInvoiceID == input.ExternalInvoiceID {
			return APIConsumerUsageStatementHandoff{}, false, ErrConflict
		}
	}
	handoff := APIConsumerUsageStatementHandoff{
		ID: uuid.NewString(), AccountID: input.AccountID, AppID: input.AppID,
		ConsumerID: input.ConsumerID, StatementID: input.StatementID,
		ExternalInvoiceID: input.ExternalInvoiceID, Currency: statement.Currency,
		AmountMillicents: statement.AmountMillicents, CreatedAt: time.Now().UTC(),
	}
	m.apiConsumerUsageStatementHandoffs[input.StatementID] = handoff
	return cloneAPIConsumerUsageStatementHandoff(handoff), true, nil
}

func (m *MemStore) GetAPIConsumerUsageStatementHandoff(_ context.Context, accountID, appID, consumerID, statementID string) (APIConsumerUsageStatementHandoff, error) {
	if accountID == "" || appID == "" || consumerID == "" || statementID == "" {
		return APIConsumerUsageStatementHandoff{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	handoff, ok := m.apiConsumerUsageStatementHandoffs[statementID]
	if !ok || handoff.AccountID != accountID || handoff.AppID != appID || handoff.ConsumerID != consumerID {
		return APIConsumerUsageStatementHandoff{}, ErrNotFound
	}
	return cloneAPIConsumerUsageStatementHandoff(handoff), nil
}
