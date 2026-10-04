package state

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialStore = (*MemStore)(nil)

type financialSamplingWindow struct {
	compute, egress bool
	observedAt      time.Time
}

func (m *MemStore) AppendFinancialAdjustment(_ context.Context, adjustment FinancialAdjustment) (FinancialUsageRecord, error) {
	if !validFinancialAdjustment(adjustment) {
		return FinancialUsageRecord{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var original FinancialUsageRecord
	var existing FinancialUsageRecord
	remaining := new(big.Int)
	for _, row := range m.financialEvidence {
		if row.Evidence.AccountID != adjustment.AccountID {
			continue
		}
		if row.Evidence.SourceID == adjustment.SourceID {
			existing = row
		}
		if row.Evidence.SourceID == adjustment.CorrectsSourceID {
			original = row
		}
		if row.Evidence.CorrectsSourceID == adjustment.CorrectsSourceID {
			remaining.Add(remaining, big.NewInt(row.Evidence.Quantity))
		}
	}
	if original.Sequence == 0 {
		return FinancialUsageRecord{}, ErrNotFound
	}
	if original.Evidence.CorrectsSourceID != "" {
		return FinancialUsageRecord{}, ErrInvalidArgument
	}
	if existing.Sequence != 0 {
		if existing.Evidence.CorrectsSourceID != adjustment.CorrectsSourceID || existing.Evidence.Quantity != adjustment.Quantity || existing.AdjustmentActor != adjustment.Actor || existing.AdjustmentReason != adjustment.Reason {
			return FinancialUsageRecord{}, ErrConflict
		}
		return existing, nil
	}
	remaining.Add(remaining, big.NewInt(original.Evidence.Quantity))
	remaining.Add(remaining, big.NewInt(adjustment.Quantity))
	if remaining.Sign() < 0 {
		return FinancialUsageRecord{}, ErrInvalidArgument
	}
	m.financialNextSequence++
	row := original
	row.Sequence = m.financialNextSequence
	row.Evidence.ID = strconv.FormatInt(row.Sequence, 10)
	row.Evidence.SourceID, row.Evidence.CorrectsSourceID, row.Evidence.Quantity = adjustment.SourceID, adjustment.CorrectsSourceID, adjustment.Quantity
	row.Evidence.ObservedAt = time.Now().UTC()
	row.AdjustmentActor, row.AdjustmentReason = adjustment.Actor, adjustment.Reason
	m.financialEvidence = append(m.financialEvidence, row)
	return row, nil
}

func (m *MemStore) retainFinancialUsageLocked(previous, current usageMinute) {
	account, ok := m.accounts[current.AccountID]
	if !ok {
		return
	} // Legacy test fixtures have no owning account.
	plan := account.Plan
	// The sampler closes the previous minute after recording current terms.
	// Use the last recorded activation for that interval, never today's plan.
	if price := m.financialPriceAtLocked(current.AccountID, "compute", current.Minute, ""); price.Price.Version != "" {
		plan = price.Plan
	}
	a := financial.Attribution{AppID: current.AppID}
	if current.MeterKind == "job" {
		a.AppID = ""
		a.JobID = current.JobID
		a.Name = m.jobs[current.JobID].Name
	} else {
		app := m.apps[current.AppID]
		a.Name = app.Slug
		a.ProjectID = app.ProjectID
	}
	if ins, exists := m.instances[current.InstanceID]; exists {
		a.DeploymentID = ins.DeploymentID
		deployment := m.deployments[ins.DeploymentID]
		for _, environment := range m.projectEnvironments {
			if environment.AccountID == current.AccountID && environment.ProjectID == a.ProjectID && environment.Slug == deployment.Scope {
				a.EnvironmentID = environment.ID
				break
			}
		}
	}
	for _, r := range m.financialEvidence {
		if r.Evidence.AccountID == current.AccountID && r.InstanceID == current.InstanceID && r.Evidence.Start.Equal(current.Minute) {
			a = r.Evidence.Attribution
			plan = r.Plan
			break
		}
	}
	for _, meter := range []struct {
		name, unit           string
		quantity, cumulative int64
	}{
		{"compute", "mb_seconds", current.MBSeconds - previous.MBSeconds, current.MBSeconds},
		{"egress", "interface_bytes", current.NetTxBytes - previous.NetTxBytes, current.NetTxBytes},
	} {
		if meter.quantity <= 0 {
			continue
		}
		m.financialNextSequence++
		source := fmt.Sprintf("usage:%s:%d:%s:%d", current.InstanceID, current.Minute.Unix(), meter.name, meter.cumulative)
		price := m.financialPriceAtLocked(current.AccountID, meter.name, current.Minute, plan)
		m.financialEvidence = append(m.financialEvidence, FinancialUsageRecord{Sequence: m.financialNextSequence, InstanceID: current.InstanceID, Plan: plan, Unit: meter.unit, PriceVersion: price.Price.Version, Evidence: financial.Evidence{ID: strconv.FormatInt(m.financialNextSequence, 10), AccountID: current.AccountID, SourceID: source, Meter: meter.name, Quantity: meter.quantity, Start: current.Minute.UTC(), End: current.Minute.Add(time.Minute).UTC(), ObservedAt: time.Now().UTC(), Attribution: a}})
	}
}

func (m *MemStore) financialPriceAtLocked(account, meter string, minute time.Time, plan api.Plan) FinancialPriceSnapshot {
	var price FinancialPriceSnapshot
	for _, p := range m.financialPrices {
		if p.AccountID != account || (plan != "" && p.Plan != plan) || p.Price.Meter != meter || p.PeriodStart.After(minute) || !p.PeriodEnd.After(minute) || p.EffectiveFrom.After(minute) {
			continue
		}
		if price.Price.Version == "" || p.EffectiveFrom.After(price.EffectiveFrom) || (p.EffectiveFrom.Equal(price.EffectiveFrom) && (p.RecordedAt.After(price.RecordedAt) || (p.RecordedAt.Equal(price.RecordedAt) && p.Price.Version < price.Price.Version))) {
			price = p
		}
	}
	return price
}

func (m *MemStore) FinancialEvidenceHead(_ context.Context, account string, start, end time.Time) (int64, error) {
	if !validFinancialWindow(account, start, end) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var head int64
	for _, r := range m.financialEvidence {
		if r.Evidence.AccountID == account && !r.Evidence.Start.Before(start) && r.Evidence.Start.Before(end) {
			head = max(head, r.Sequence)
		}
	}
	return head, nil
}

func (m *MemStore) FinancialEvidenceCoverage(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.financialRetainedFrom, nil
}

func (m *MemStore) ListFinancialUsageEvidence(_ context.Context, account string, start, end time.Time, after, through int64, limit int) ([]FinancialUsageRecord, error) {
	if !validFinancialWindow(account, start, end) || after < 0 || through < after || limit < 1 || limit > api.FinancialEvidencePageMax {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialUsageRecord{}
	for _, r := range m.financialEvidence {
		if r.Evidence.AccountID == account && !r.Evidence.Start.Before(start) && r.Evidence.Start.Before(end) && r.Sequence > after && r.Sequence <= through {
			out = append(out, r)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func financialPriceKey(p FinancialPriceSnapshot) string {
	return p.AccountID + "\x00" + p.PeriodStart.UTC().Format(time.RFC3339Nano) + "\x00" + p.Price.Meter + "\x00" + p.Price.Version
}

func (m *MemStore) PutFinancialPriceSnapshot(_ context.Context, p FinancialPriceSnapshot) (FinancialPriceSnapshot, error) {
	if !validFinancialPrice(p) {
		return FinancialPriceSnapshot{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[p.AccountID]; !ok {
		return FinancialPriceSnapshot{}, ErrNotFound
	}
	if m.financialPrices == nil {
		m.financialPrices = map[string]FinancialPriceSnapshot{}
	}
	key := financialPriceKey(p)
	if old, ok := m.financialPrices[key]; ok {
		if old.Price != p.Price || !old.PeriodEnd.Equal(p.PeriodEnd) || old.Plan != p.Plan || old.DeliveryMode != p.DeliveryMode || !old.EffectiveFrom.Equal(p.EffectiveFrom) {
			return FinancialPriceSnapshot{}, ErrConflict
		}
		return old, nil
	}
	p.PeriodStart = p.PeriodStart.UTC()
	p.PeriodEnd = p.PeriodEnd.UTC()
	p.EffectiveFrom = p.EffectiveFrom.UTC()
	p.RecordedAt = time.Now().UTC()
	m.financialPrices[key] = p
	return p, nil
}

func (m *MemStore) ListFinancialPriceSnapshots(_ context.Context, account string, start time.Time) ([]FinancialPriceSnapshot, error) {
	if account == "" || start.IsZero() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialPriceSnapshot{}
	for _, p := range m.financialPrices {
		if p.AccountID == account && p.PeriodStart.Equal(start) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Price.Meter != out[j].Price.Meter {
			return out[i].Price.Meter < out[j].Price.Meter
		}
		return out[i].Price.Version < out[j].Price.Version
	})
	return out, nil
}

func (m *MemStore) RecordFinancialSamplingWindow(_ context.Context, minute time.Time, compute, egress bool) error {
	if minute.IsZero() || !minute.Equal(minute.Truncate(time.Minute)) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.financialSamplingWindows == nil {
		m.financialSamplingWindows = map[time.Time]financialSamplingWindow{}
	}
	minute = minute.UTC()
	old := m.financialSamplingWindows[minute]
	m.financialSamplingWindows[minute] = financialSamplingWindow{compute: old.compute || compute, egress: old.egress || egress, observedAt: time.Now().UTC()}
	return nil
}

func (m *MemStore) FinancialSamplingCoverage(_ context.Context, start, end time.Time) (FinancialSamplingCoverage, error) {
	if !validFinancialSamplingWindow(start, end) {
		return FinancialSamplingCoverage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out FinancialSamplingCoverage
	for minute, row := range m.financialSamplingWindows {
		if minute.Before(start) || !minute.Before(end) {
			continue
		}
		if row.compute {
			out.ComputeMinutes++
		}
		if row.egress {
			out.EgressMinutes++
		}
		if row.observedAt.After(out.ObservedAt) {
			out.ObservedAt = row.observedAt
		}
	}
	return out, nil
}

func (m *MemStore) AggregateFinancialUsage(_ context.Context, account string, start, end time.Time, through int64) ([]FinancialUsageAggregate, error) {
	if account == "" || through < 0 || !validFinancialSamplingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		price, plan, meter, unit string
		attribution              financial.Attribution
	}
	groups := map[key]FinancialUsageAggregate{}
	quantities := map[key]*big.Int{}
	for _, row := range m.financialEvidence {
		e := row.Evidence
		if e.AccountID != account || row.Sequence > through || e.Start.Before(start) || e.End.After(end) {
			continue
		}
		k := key{row.PriceVersion, string(row.Plan), e.Meter, row.Unit, e.Attribution}
		if quantities[k] == nil {
			quantities[k] = new(big.Int)
		}
		quantities[k].Add(quantities[k], big.NewInt(e.Quantity))
		group := groups[k]
		group.PriceVersion, group.Plan, group.Meter, group.Unit, group.Attribution = row.PriceVersion, row.Plan, e.Meter, row.Unit, e.Attribution
		group.SourceCount++
		groups[k] = group
	}
	if len(groups) > api.FinancialAllocationMax {
		return nil, ErrFinancialAllocationLimit
	}
	out := make([]FinancialUsageAggregate, 0, len(groups))
	for k, group := range groups {
		if !quantities[k].IsInt64() {
			return nil, financial.ErrOverflow
		}
		group.Quantity = quantities[k].Int64()
		out = append(out, group)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Meter+"\x00"+a.PriceVersion+"\x00"+string(a.Plan)+"\x00"+a.Unit+"\x00"+fmt.Sprint(a.Attribution) < b.Meter+"\x00"+b.PriceVersion+"\x00"+string(b.Plan)+"\x00"+b.Unit+"\x00"+fmt.Sprint(b.Attribution)
	})
	return out, nil
}
