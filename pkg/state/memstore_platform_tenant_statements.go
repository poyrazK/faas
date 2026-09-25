package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) ListPlatformTenantUsageMinutes(_ context.Context, accountID, tenantID string, start, end time.Time) ([]APIConsumerUsageBucket, error) {
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []APIConsumerUsageBucket{}
	prefix := accountID + "\x00" + tenantID + "\x00"
	for key, bucket := range m.platformTenantUsage {
		if strings.HasPrefix(key, prefix) && !bucket.WindowStart.Before(start) && bucket.WindowStart.Before(end) {
			out = append(out, bucket)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		if out[i].ConsumerKey != out[j].ConsumerKey {
			return out[i].ConsumerKey < out[j].ConsumerKey
		}
		if out[i].SurfaceID != out[j].SurfaceID {
			return out[i].SurfaceID < out[j].SurfaceID
		}
		return out[i].WindowStart.Before(out[j].WindowStart)
	})
	return out, nil
}

func (m *MemStore) CreatePlatformTenantStatement(_ context.Context, in PlatformTenantStatementInput) (PlatformTenantStatement, bool, error) {
	if err := validatePlatformTenantStatementInput(in); err != nil {
		return PlatformTenantStatement{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[in.TenantID]; !ok || tenant.AccountID != in.AccountID {
		return PlatformTenantStatement{}, false, ErrNotFound
	}
	latest := PlatformTenantStatement{}
	for _, statement := range m.platformTenantStatements {
		if statement.TenantID != in.TenantID || !statement.PeriodStart.Equal(in.PeriodStart) || !statement.PeriodEnd.Equal(in.PeriodEnd) {
			continue
		}
		if statement.Revision == in.Revision {
			return clonePlatformTenantStatement(statement), false, nil
		}
		if statement.Revision > latest.Revision {
			latest = statement
		}
	}
	if in.Revision != latest.Revision+1 || latest.Status != in.PriorStatus {
		return PlatformTenantStatement{}, false, ErrConflict
	}
	if latest.Status == APIConsumerUsageStatementDraft {
		latest.Status = PlatformTenantStatementSuperseded
		m.platformTenantStatements[latest.ID] = latest
	}
	now := time.Now().UTC()
	out := PlatformTenantStatement{ID: uuid.NewString(), AccountID: in.AccountID, TenantID: in.TenantID,
		PeriodStart: in.PeriodStart, PeriodEnd: in.PeriodEnd, Revision: in.Revision,
		Status: APIConsumerUsageStatementDraft, Currency: in.Currency, BillableUnits: in.BillableUnits,
		UnpricedUnits: in.UnpricedUnits, AmountMillicents: in.AmountMillicents,
		Lines: append([]PlatformTenantStatementLine(nil), in.Lines...), AsOf: in.AsOf, CreatedAt: now}
	m.platformTenantStatements[out.ID] = out
	return clonePlatformTenantStatement(out), true, nil
}

func (m *MemStore) GetPlatformTenantStatement(_ context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.platformTenantStatements[statementID]
	if !ok || out.AccountID != accountID || out.TenantID != tenantID {
		return PlatformTenantStatement{}, ErrNotFound
	}
	return clonePlatformTenantStatement(out), nil
}

func (m *MemStore) ListPlatformTenantStatements(_ context.Context, accountID, tenantID string, start, end time.Time) ([]PlatformTenantStatement, error) {
	if !end.After(start) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []PlatformTenantStatement{}
	for _, statement := range m.platformTenantStatements {
		if statement.AccountID == accountID && statement.TenantID == tenantID && statement.PeriodStart.Equal(start) && statement.PeriodEnd.Equal(end) {
			out = append(out, clonePlatformTenantStatement(statement))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

func (m *MemStore) FinalizePlatformTenantStatement(_ context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.platformTenantStatements[statementID]
	if !ok || out.AccountID != accountID || out.TenantID != tenantID {
		return PlatformTenantStatement{}, false, ErrNotFound
	}
	if out.Status == APIConsumerUsageStatementFinalized {
		return clonePlatformTenantStatement(out), false, nil
	}
	if out.UnpricedUnits > 0 || out.Currency == "" {
		return PlatformTenantStatement{}, false, ErrConflict
	}
	now := time.Now().UTC()
	out.Status, out.FinalizedAt = APIConsumerUsageStatementFinalized, &now
	m.platformTenantStatements[out.ID] = out
	return clonePlatformTenantStatement(out), true, nil
}

func tenantStatementIncludesConsumer(statement PlatformTenantStatement, appID, consumerID string) bool {
	if consumerID == "" {
		return false
	}
	for _, line := range statement.Lines {
		if line.AppID == appID && line.ConsumerID == consumerID {
			return true
		}
	}
	return false
}

func tenantStatementIncludesSurface(statement PlatformTenantStatement, appID, surfaceID string) bool {
	if surfaceID == "" {
		return false
	}
	for _, line := range statement.Lines {
		if line.AppID == appID && line.SurfaceID == surfaceID {
			return true
		}
	}
	return false
}

func windowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && aEnd.After(bStart)
}

func (m *MemStore) CreatePlatformTenantStatementHandoff(_ context.Context, in PlatformTenantStatementHandoffInput) (PlatformTenantStatementHandoff, bool, error) {
	if err := validatePlatformTenantHandoffInput(in); err != nil {
		return PlatformTenantStatementHandoff{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	statement, ok := m.platformTenantStatements[in.StatementID]
	if !ok || statement.AccountID != in.AccountID || statement.TenantID != in.TenantID {
		return PlatformTenantStatementHandoff{}, false, ErrNotFound
	}
	if existing, ok := m.platformTenantStatementHandoffs[in.StatementID]; ok {
		if existing.ExternalInvoiceID != in.ExternalInvoiceID {
			return PlatformTenantStatementHandoff{}, false, ErrConflict
		}
		return existing, false, nil
	}
	if statement.Status != APIConsumerUsageStatementFinalized || statement.Currency == "" {
		return PlatformTenantStatementHandoff{}, false, ErrConflict
	}
	for _, h := range m.apiConsumerUsageStatementHandoffs {
		other := m.apiConsumerUsageStatements[h.StatementID]
		if h.AccountID == in.AccountID && (h.ExternalInvoiceID == in.ExternalInvoiceID ||
			(windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) &&
				tenantStatementIncludesConsumer(statement, other.AppID, other.ConsumerID))) {
			return PlatformTenantStatementHandoff{}, false, ErrConflict
		}
	}
	for _, h := range m.platformTenantStatementHandoffs {
		other := m.platformTenantStatements[h.StatementID]
		if h.AccountID != in.AccountID {
			continue
		}
		if h.ExternalInvoiceID == in.ExternalInvoiceID {
			return PlatformTenantStatementHandoff{}, false, ErrConflict
		}
		if other.TenantID == statement.TenantID && other.PeriodStart.Equal(statement.PeriodStart) && other.PeriodEnd.Equal(statement.PeriodEnd) {
			continue
		}
		if !windowsOverlap(statement.PeriodStart, statement.PeriodEnd, other.PeriodStart, other.PeriodEnd) {
			continue
		}
		for _, line := range statement.Lines {
			if tenantStatementIncludesConsumer(other, line.AppID, line.ConsumerID) ||
				tenantStatementIncludesSurface(other, line.AppID, line.SurfaceID) {
				return PlatformTenantStatementHandoff{}, false, ErrConflict
			}
		}
	}
	out := PlatformTenantStatementHandoff{ID: uuid.NewString(), AccountID: in.AccountID, TenantID: in.TenantID,
		StatementID: in.StatementID, ExternalInvoiceID: in.ExternalInvoiceID, Currency: statement.Currency,
		AmountMillicents: statement.AmountMillicents, CreatedAt: time.Now().UTC()}
	m.platformTenantStatementHandoffs[in.StatementID] = out
	return out, true, nil
}

func (m *MemStore) GetPlatformTenantStatementHandoff(_ context.Context, accountID, tenantID, statementID string) (PlatformTenantStatementHandoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.platformTenantStatementHandoffs[statementID]
	if !ok || out.AccountID != accountID || out.TenantID != tenantID {
		return PlatformTenantStatementHandoff{}, ErrNotFound
	}
	return out, nil
}
