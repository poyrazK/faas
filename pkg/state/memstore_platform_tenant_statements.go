package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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
		if out[i].JWTAuthorizationRuleID != out[j].JWTAuthorizationRuleID {
			return out[i].JWTAuthorizationRuleID < out[j].JWTAuthorizationRuleID
		}
		return out[i].WindowStart.Before(out[j].WindowStart)
	})
	return out, nil
}

func (m *MemStore) PlanPlatformTenantStatement(_ context.Context, accountID, tenantID string, start, end time.Time) (PlatformTenantStatementPlan, error) {
	if !end.After(start) {
		return PlatformTenantStatementPlan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return PlatformTenantStatementPlan{}, ErrNotFound
	}

	plan := PlatformTenantStatementPlan{}
	covered := map[string]int64{}
	for _, statement := range m.platformTenantStatements {
		if statement.AccountID != accountID || statement.TenantID != tenantID ||
			!statement.PeriodStart.Equal(start) || !statement.PeriodEnd.Equal(end) {
			continue
		}
		if !plan.HasLatest || statement.Revision > plan.Latest.Revision {
			plan.Latest = clonePlatformTenantStatementHeader(statement)
			plan.HasLatest = true
		}
		if statement.Status != APIConsumerUsageStatementFinalized {
			continue
		}
		coverage := statement.Coverage
		if len(coverage) == 0 {
			for _, line := range statement.Lines {
				if !line.WindowEnd.IsZero() && !line.WindowEnd.Equal(line.WindowStart.Add(time.Minute)) {
					return PlatformTenantStatementPlan{}, ErrPlatformTenantUsageRegressed
				}
				coverage = append(coverage, PlatformTenantStatementCoverage{
					AppID: line.AppID, ConsumerID: line.ConsumerID, SurfaceID: line.SurfaceID,
					JWTAuthorizationRuleID: line.JWTAuthorizationRuleID,
					WindowStart:            line.WindowStart, BillableUnits: line.BillableUnits,
				})
			}
		}
		for _, minute := range coverage {
			key := platformTenantCoverageKey(minute.AppID, minute.ConsumerID, minute.SurfaceID, minute.JWTAuthorizationRuleID, minute.WindowStart)
			if covered[key] > maxAPIConsumerUsageStatementInt64-minute.BillableUnits {
				return PlatformTenantStatementPlan{}, fmt.Errorf("state: platform tenant coverage overflow")
			}
			covered[key] += minute.BillableUnits
		}
	}

	prefix := accountID + "\x00" + tenantID + "\x00"
	for key, bucket := range m.platformTenantUsage {
		if !strings.HasPrefix(key, prefix) || bucket.WindowStart.Before(start) || !bucket.WindowStart.Before(end) {
			continue
		}
		coverageKey := platformTenantCoverageKey(bucket.AppID, bucket.ConsumerKey, bucket.SurfaceID, bucket.JWTAuthorizationRuleID, bucket.WindowStart)
		prior := covered[coverageKey]
		if bucket.BillableUnits < prior {
			return PlatformTenantStatementPlan{}, ErrPlatformTenantUsageRegressed
		}
		delete(covered, coverageKey)
		bucket.BillableUnits -= prior
		if bucket.BillableUnits > 0 {
			plan.UsageDelta = append(plan.UsageDelta, bucket)
		}
	}
	for _, prior := range covered {
		if prior > 0 {
			return PlatformTenantStatementPlan{}, ErrPlatformTenantUsageRegressed
		}
	}
	sort.Slice(plan.UsageDelta, func(i, j int) bool {
		left, right := plan.UsageDelta[i], plan.UsageDelta[j]
		if left.AppID != right.AppID {
			return left.AppID < right.AppID
		}
		if left.ConsumerKey != right.ConsumerKey {
			return left.ConsumerKey < right.ConsumerKey
		}
		if left.SurfaceID != right.SurfaceID {
			return left.SurfaceID < right.SurfaceID
		}
		if left.JWTAuthorizationRuleID != right.JWTAuthorizationRuleID {
			return left.JWTAuthorizationRuleID < right.JWTAuthorizationRuleID
		}
		return left.WindowStart.Before(right.WindowStart)
	})
	return plan, nil
}

func (m *MemStore) CreatePlatformTenantStatement(_ context.Context, in PlatformTenantStatementInput) (PlatformTenantStatement, bool, error) {
	in = normalizePlatformTenantStatementInput(in)
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
			return clonePlatformTenantStatementHeader(statement), false, nil
		}
		if statement.Revision > latest.Revision {
			latest = statement
		}
	}
	if in.Revision != latest.Revision+1 || latest.Status != in.PriorStatus {
		return PlatformTenantStatement{}, false, ErrConflict
	}
	if latest.Status == APIConsumerUsageStatementDraft && samePlatformTenantStatementSnapshot(latest, in) {
		return clonePlatformTenantStatementHeader(latest), false, nil
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
		Lines:    append([]PlatformTenantStatementLine(nil), in.Lines...),
		Coverage: append([]PlatformTenantStatementCoverage(nil), in.Coverage...),
		AsOf:     in.AsOf, CreatedAt: now}
	m.platformTenantStatements[out.ID] = out
	return clonePlatformTenantStatementHeader(out), true, nil
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

func (m *MemStore) GetPlatformTenantStatementHeader(_ context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.platformTenantStatements[statementID]
	if !ok || out.AccountID != accountID || out.TenantID != tenantID {
		return PlatformTenantStatement{}, ErrNotFound
	}
	return clonePlatformTenantStatementHeader(out), nil
}

func (m *MemStore) ListPlatformTenantStatementHeaders(_ context.Context, accountID, tenantID string, start, end time.Time) ([]PlatformTenantStatement, error) {
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
		if statement.AccountID == accountID && statement.TenantID == tenantID &&
			statement.PeriodStart.Equal(start) && statement.PeriodEnd.Equal(end) {
			out = append(out, clonePlatformTenantStatementHeader(statement))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

func (m *MemStore) ListFinalizedPlatformTenantStatements(_ context.Context, accountID, tenantID string, start, end time.Time, limit, offset int) ([]PlatformTenantStatementSummary, error) {
	if !end.After(start) || limit < 1 || limit > 101 || offset < 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := make([]PlatformTenantStatementSummary, 0)
	for _, statement := range m.platformTenantStatements {
		if statement.AccountID == accountID && statement.TenantID == tenantID &&
			statement.Status == APIConsumerUsageStatementFinalized && statement.PeriodStart.Before(end) && statement.PeriodEnd.After(start) {
			out = append(out, platformTenantStatementSummary(statement))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PeriodStart.Equal(out[j].PeriodStart) {
			return out[i].PeriodStart.After(out[j].PeriodStart)
		}
		if out[i].Revision != out[j].Revision {
			return out[i].Revision > out[j].Revision
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if offset >= len(out) {
		return []PlatformTenantStatementSummary{}, nil
	}
	endIndex := offset + limit
	if endIndex > len(out) {
		endIndex = len(out)
	}
	return out[offset:endIndex], nil
}

func (m *MemStore) FinalizePlatformTenantStatement(_ context.Context, accountID, tenantID, statementID string) (PlatformTenantStatement, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.platformTenantStatements[statementID]
	if !ok || out.AccountID != accountID || out.TenantID != tenantID {
		return PlatformTenantStatement{}, false, ErrNotFound
	}
	if out.Status == APIConsumerUsageStatementFinalized {
		return clonePlatformTenantStatementHeader(out), false, nil
	}
	if out.UnpricedUnits > 0 || out.Currency == "" {
		return PlatformTenantStatement{}, false, ErrConflict
	}
	now := time.Now().UTC()
	out.Status, out.FinalizedAt = APIConsumerUsageStatementFinalized, &now
	m.platformTenantStatements[out.ID] = out
	m.enqueuePlatformTenantStatementFinalizedWebhooksLocked(out, now)
	return clonePlatformTenantStatementHeader(out), true, nil
}

func (m *MemStore) enqueuePlatformTenantStatementFinalizedWebhooksLocked(statement PlatformTenantStatement, now time.Time) {
	tenant, ok := m.platformTenants[statement.TenantID]
	if !ok || tenant.AccountID != statement.AccountID || statement.FinalizedAt == nil {
		return
	}
	lines := make([]api.PlatformTenantStatementLineResponse, 0, len(statement.Lines))
	for _, line := range statement.Lines {
		lines = append(lines, api.PlatformTenantStatementLineResponse{
			AppID: line.AppID, ConsumerID: line.ConsumerID, SurfaceID: line.SurfaceID,
			JWTAuthorizationRuleID: line.JWTAuthorizationRuleID, WindowStart: line.WindowStart, WindowEnd: line.WindowEnd,
			BillableUnits: line.BillableUnits, RateCardID: line.RateCardID,
			PlatformTenantRateCardID: line.PlatformTenantRateCardID, Currency: line.Currency,
			PriceMillicentsPerUnit: line.PriceMillicentsPerUnit, AmountMillicents: line.AmountMillicents,
		})
	}
	payload, err := json.Marshal(api.PlatformTenantStatementFinalizedWebhookPayload{
		PlatformTenantID: tenant.ID, ExternalRef: tenant.ExternalRef, StatementID: statement.ID,
		Revision: statement.Revision, Status: string(statement.Status), PeriodStart: statement.PeriodStart,
		PeriodEnd: statement.PeriodEnd, Currency: statement.Currency, BillableUnits: statement.BillableUnits,
		UnpricedUnits: statement.UnpricedUnits, AmountMillicents: statement.AmountMillicents,
		Priced: statement.UnpricedUnits == 0 && statement.Currency != "", Lines: lines,
		AsOf: statement.AsOf, FinalizedAt: *statement.FinalizedAt,
	})
	if err != nil {
		return
	}
	event := AppWebhookEventPlatformTenantStatementFinalized
	for _, hook := range m.appWebhooks {
		if hook.Scope != AppWebhookScopePlatformTenant || hook.PlatformTenantID != tenant.ID ||
			hook.AccountID != tenant.AccountID || !hook.Enabled || !appWebhookMatches(hook.EventFilter, event) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: hook.ID, AccountID: tenant.AccountID,
			Event: event, Payload: json.RawMessage(payload), Status: AppWebhookDeliveryPending,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
	}
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

func tenantStatementIncludesJWTAuthorizationRule(statement PlatformTenantStatement, appID, ruleID string) bool {
	if ruleID == "" {
		return false
	}
	for _, line := range statement.Lines {
		if line.AppID == appID && line.JWTAuthorizationRuleID == ruleID {
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
				tenantStatementIncludesSurface(other, line.AppID, line.SurfaceID) ||
				tenantStatementIncludesJWTAuthorizationRule(other, line.AppID, line.JWTAuthorizationRuleID) {
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
