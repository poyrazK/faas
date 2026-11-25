package state

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/flags"
)

// MemStore keeps experiments for apid tests; it has no request telemetry to
// read exposures from, so AdvanceFlagExperimentUnits reports that.

type memFlagExperimentUnit struct {
	variant                        string
	firstExposed                   time.Time
	converted                      bool
	crossed                        bool
	requests, errors, tested, slow int64
	valueSum                       int64
	convertedAt                    time.Time
	preValue                       *float64
	// customer and subjectHash are a subject unit's (ADR 20261119173702 §1).
	customer    string
	subjectHash []byte
	denSum      int64
}

func (fe *memFlagEvents) experimentsLocked() map[string]*FlagExperiment {
	if fe.experiments == nil {
		fe.experiments = map[string]*FlagExperiment{}
		fe.units = map[string]map[string]*memFlagExperimentUnit{}
	}
	return fe.experiments
}

func cloneFlagExperiment(x *FlagExperiment) FlagExperiment {
	out := *x
	out.Weights = map[string]int{}
	for k, v := range x.Weights {
		out.Weights[k] = v
	}
	out.MinPValues = map[string]float64{}
	for k, v := range x.MinPValues {
		out.MinPValues[k] = v
	}
	out.MinCupedPValues = map[string]float64{}
	for k, v := range x.MinCupedPValues {
		out.MinCupedPValues[k] = v
	}
	out.Bandit = slices.Clone(x.Bandit)
	out.NotifyChannelIDs = slices.Clone(x.NotifyChannelIDs)
	if x.Breach != nil {
		out.Breach = map[string]any{}
		for k, v := range x.Breach {
			out.Breach[k] = v
		}
	}
	return out
}

func (m *MemStore) memExperiment(environmentID, key string) *FlagExperiment {
	for _, x := range m.flagEventsLocked().experimentsLocked() {
		if x.EnvironmentID == environmentID && x.Key == key {
			return x
		}
	}
	return nil
}

// CreateFlagExperiment implements FlagExperimentStore.
func (m *MemStore) CreateFlagExperiment(_ context.Context, x FlagExperiment, maxExperiments int) (FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.memExperiment(x.EnvironmentID, x.Key) != nil {
		return FlagExperiment{}, ErrConflict
	}
	n := 0
	for _, e := range m.flagEventsLocked().experimentsLocked() {
		if e.EnvironmentID == x.EnvironmentID {
			n++
		}
	}
	if n >= maxExperiments {
		return FlagExperiment{}, ErrFlagExperimentLimit
	}
	if x.MetricKind == "" {
		x.MetricKind = flags.ExperimentMetricConversion
	}
	x.Unit = orDefault(x.Unit, flags.ExperimentUnitCustomer)
	x.Allocation = orDefault(x.Allocation, flags.ExperimentAllocationFixed)
	x.ID, x.Status, x.CreatedAt = uuid.NewString(), flags.ExperimentDraft, time.Now().UTC()
	stored := cloneFlagExperiment(&x)
	m.flagEvents.experiments[x.ID] = &stored
	return cloneFlagExperiment(&stored), nil
}

// GetFlagExperiment implements FlagExperimentStore.
func (m *MemStore) GetFlagExperiment(_ context.Context, environmentID, key string) (FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x := m.memExperiment(environmentID, key); x != nil {
		return cloneFlagExperiment(x), nil
	}
	return FlagExperiment{}, ErrNotFound
}

// ListFlagExperiments implements FlagExperimentStore.
func (m *MemStore) ListFlagExperiments(_ context.Context, environmentID string) ([]FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FlagExperiment{}
	for _, x := range m.flagEventsLocked().experimentsLocked() {
		if x.EnvironmentID == environmentID {
			out = append(out, cloneFlagExperiment(x))
		}
	}
	slices.SortFunc(out, func(a, b FlagExperiment) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return compareStrings(a.Key, b.Key)
	})
	return out, nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// StartFlagExperiment implements FlagExperimentStore.
func (m *MemStore) StartFlagExperiment(_ context.Context, environmentID, key string, weights map[string]int, slice [2]int, now time.Time, maxRunning int) (FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.memExperiment(environmentID, key)
	if x == nil || x.Status != flags.ExperimentDraft {
		return FlagExperiment{}, ErrNotFound
	}
	running := 0
	for _, e := range m.flagEvents.experiments {
		if e.EnvironmentID != environmentID || e.Status != flags.ExperimentRunning {
			continue
		}
		running++
		if e.FlagKey == x.FlagKey && e.RuleID == x.RuleID {
			return FlagExperiment{}, ErrConflict
		}
	}
	if running >= maxRunning {
		return FlagExperiment{}, ErrFlagExperimentLimit
	}
	at := now.UTC()
	x.Status, x.StartedAt, x.TelemetryCursor, x.Weights = flags.ExperimentRunning, &at, FlagExperimentStartCursor(at), weights
	x.LayerFrom, x.LayerTo = slice[0], slice[1]
	return cloneFlagExperiment(x), nil
}

// StopFlagExperiment implements FlagExperimentStore.
func (m *MemStore) StopFlagExperiment(_ context.Context, environmentID, key string, now time.Time, reason string, breach map[string]any) (FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.memExperiment(environmentID, key)
	if x == nil || x.Status != flags.ExperimentRunning {
		return FlagExperiment{}, ErrNotFound
	}
	at := now.UTC()
	x.Status, x.StoppedAt, x.StopReason, x.Breach = flags.ExperimentStopped, &at, reason, breach
	return cloneFlagExperiment(x), nil
}

// DeleteFlagExperiment implements FlagExperimentStore.
func (m *MemStore) DeleteFlagExperiment(_ context.Context, environmentID, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.memExperiment(environmentID, key)
	if x == nil || x.Status == flags.ExperimentRunning {
		return false, nil
	}
	delete(m.flagEvents.experiments, x.ID)
	delete(m.flagEvents.units, x.ID)
	return true, nil
}

// FlagExperimentArms implements FlagExperimentStore.
func (m *MemStore) FlagExperimentArms(_ context.Context, x FlagExperiment, minUnits int64) ([]flags.Arm, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	if x.MetricKind == flags.ExperimentMetricMean {
		return m.memMeanArmsLocked(x, minUnits)
	}
	byVariant := map[string]*flags.Arm{}
	var crossed int64
	for _, u := range m.flagEvents.units[x.ID] {
		if u.crossed {
			crossed++
			continue
		}
		if !memUnitCounted(x, u) {
			continue
		}
		a := byVariant[u.variant]
		if a == nil {
			a = &flags.Arm{Variant: u.variant}
			byVariant[u.variant] = a
		}
		a.Exposed++
		if u.converted {
			a.Converted++
		}
	}
	arms := make([]flags.Arm, 0, len(byVariant))
	for _, a := range byVariant {
		arms = append(arms, *a)
	}
	slices.SortFunc(arms, func(a, b flags.Arm) int { return compareStrings(a.Variant, b.Variant) })
	return arms, crossed, nil
}

// ListRunningFlagExperiments implements FlagExperimentStore.
func (m *MemStore) ListRunningFlagExperiments(_ context.Context, afterID string, limit int) ([]FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FlagExperiment{}
	for _, x := range m.flagEventsLocked().experimentsLocked() {
		if x.Status == flags.ExperimentRunning && x.ID > afterID {
			c := cloneFlagExperiment(x)
			if acct, ok := m.accounts[x.AccountID]; ok {
				c.Plan = string(acct.Plan)
			}
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b FlagExperiment) int { return compareStrings(a.ID, b.ID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// memMeanArmsLocked mirrors FlagExperimentMeanArms: folded values plus open
// days, clamped to the 1st and 99th percentile (continuous interpolation,
// like percentile_cont) once there are minUnits units.
func (m *MemStore) memMeanArmsLocked(x FlagExperiment, minUnits int64) ([]flags.Arm, int64, error) {
	open := flagExperimentOpenFromDay(x)
	type val struct {
		variant string
		v       float64
	}
	var vals []val
	var crossed int64
	for customer, u := range m.flagEvents.units[x.ID] {
		if u.crossed {
			crossed++
			continue
		}
		v := float64(u.valueSum)
		for k, d := range m.flagEvents.daily {
			if k.environment == x.EnvironmentID && k.name == x.MetricEvent && k.customer == customer &&
				!d.Day.Before(open) && !d.LastAt.Before(u.firstExposed) {
				v += float64(d.ValueSum)
			}
		}
		vals = append(vals, val{u.variant, v})
	}
	sorted := make([]float64, len(vals))
	for i, v := range vals {
		sorted[i] = v.v
	}
	slices.Sort(sorted)
	lo, hi := percentileCont(sorted, 0.01), percentileCont(sorted, 0.99)
	byVariant := map[string]*flags.Arm{}
	for _, v := range vals {
		a := byVariant[v.variant]
		if a == nil {
			a = &flags.Arm{Variant: v.variant}
			byVariant[v.variant] = a
		}
		w := v.v
		if int64(len(vals)) >= minUnits {
			w = min(max(w, lo), hi)
		}
		a.Exposed++
		if v.v != 0 {
			a.Converted++
		}
		a.Sum += w
		a.SumSq += w * w
	}
	arms := make([]flags.Arm, 0, len(byVariant))
	for _, a := range byVariant {
		arms = append(arms, *a)
	}
	slices.SortFunc(arms, func(a, b flags.Arm) int { return compareStrings(a.Variant, b.Variant) })
	return arms, crossed, nil
}

// percentileCont is Postgres percentile_cont over sorted values.
func percentileCont(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := q * float64(len(sorted)-1)
	i := int(pos)
	if i+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[i] + (pos-float64(i))*(sorted[i+1]-sorted[i])
}

// FlagExperimentGuardrailArms implements FlagExperimentStore.
func (m *MemStore) FlagExperimentGuardrailArms(_ context.Context, experimentID string) ([]flags.RatioArm, []flags.RatioArm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	errs, slow := map[string]*flags.RatioArm{}, map[string]*flags.RatioArm{}
	add := func(arms map[string]*flags.RatioArm, variant string, num, den int64) {
		a := arms[variant]
		if a == nil {
			a = &flags.RatioArm{Variant: variant}
			arms[variant] = a
		}
		if den > 0 {
			a.Units++
		}
		n, d := float64(num), float64(den)
		a.Num, a.Den, a.NumSq, a.DenSq, a.NumDen = a.Num+n, a.Den+d, a.NumSq+n*n, a.DenSq+d*d, a.NumDen+n*d
	}
	for _, u := range m.flagEvents.units[experimentID] {
		if u.crossed {
			continue
		}
		add(errs, u.variant, u.errors, u.requests)
		add(slow, u.variant, u.slow, u.tested)
	}
	list := func(arms map[string]*flags.RatioArm) []flags.RatioArm {
		out := make([]flags.RatioArm, 0, len(arms))
		for _, a := range arms {
			out = append(out, *a)
		}
		slices.SortFunc(out, func(a, b flags.RatioArm) int { return compareStrings(a.Variant, b.Variant) })
		return out
	}
	return list(errs), list(slow), nil
}

// FlagExperimentLatency implements FlagExperimentStore.
func (m *MemStore) FlagExperimentLatency(_ context.Context, experimentID string) (map[string]map[int]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	out := map[string]map[int]int64{}
	for variant, h := range fe.latency[experimentID] {
		out[variant] = maps.Clone(h)
	}
	return out, nil
}

// SetFlagExperimentLatencyThreshold implements FlagExperimentStore.
func (m *MemStore) SetFlagExperimentLatencyThreshold(_ context.Context, experimentID string, ms int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.flagEventsLocked().experimentsLocked()[experimentID]
	if x == nil || x.Status != flags.ExperimentRunning || x.LatencyThresholdMS != nil {
		return nil
	}
	x.LatencyThresholdMS = &ms
	return nil
}

// FoldFlagExperimentValues implements FlagExperimentStore.
func (m *MemStore) FoldFlagExperimentValues(_ context.Context, x FlagExperiment, through time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.flagEventsLocked().experimentsLocked()[x.ID]
	if stored == nil || stored.Status != flags.ExperimentRunning {
		return nil
	}
	from, through := flagExperimentOpenFromDay(*stored), utcDay(through)
	if through.Before(from) {
		return nil
	}
	for customer, u := range m.flagEvents.units[x.ID] {
		for k, d := range m.flagEvents.daily {
			if k.environment == x.EnvironmentID && k.name == x.MetricEvent && k.customer == customer &&
				!d.Day.Before(from) && !d.Day.After(through) && !d.LastAt.Before(u.firstExposed) {
				u.valueSum += d.ValueSum
			}
		}
	}
	stored.ValueThroughDay = &through
	return nil
}

// SeedFlagExperimentUnitForTest adds a unit with guardrail counts, and its requests
// to the variant's latency histogram at latencyMS, for apid tests: MemStore
// keeps no request telemetry to read units from.
func (m *MemStore) SeedFlagExperimentUnitForTest(experimentID, customerID, variant string, firstExposed time.Time, requests, errors, tested, slow, latencyMS int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	fe.experimentsLocked()
	if fe.units[experimentID] == nil {
		fe.units[experimentID] = map[string]*memFlagExperimentUnit{}
	}
	fe.units[experimentID][customerID] = &memFlagExperimentUnit{variant: variant, firstExposed: firstExposed.UTC(),
		requests: requests, errors: errors, tested: tested, slow: slow}
	if fe.latency == nil {
		fe.latency = map[string]map[string]map[int]int64{}
	}
	if fe.latency[experimentID] == nil {
		fe.latency[experimentID] = map[string]map[int]int64{}
	}
	if fe.latency[experimentID][variant] == nil {
		fe.latency[experimentID][variant] = map[int]int64{}
	}
	fe.latency[experimentID][variant][flags.LatencyBucket(latencyMS)] += requests
}

// AdvanceFlagExperimentUnits reads exposures from request telemetry, which
// MemStore does not keep (ADR-127): like the other telemetry readers it
// returns errMemStoreRequestTelemetry, and unit tracking is tested against
// Postgres (TestPg_FlagExperimentUnitsFromTelemetry).
func (m *MemStore) AdvanceFlagExperimentUnits(context.Context, FlagExperiment, time.Time, time.Time, int) (int, error) {
	return 0, errMemStoreRequestTelemetry
}

// SaveFlagExperimentProgress implements FlagExperimentStore.
func (m *MemStore) SaveFlagExperimentProgress(_ context.Context, experimentID string, cursor time.Time, minPValues, minCupedPValues map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x := m.flagEventsLocked().experimentsLocked()[experimentID]; x != nil && x.Status == flags.ExperimentRunning {
		x.TelemetryCursor, x.MinPValues, x.MinCupedPValues = cursor, minPValues, minCupedPValues
	}
	return nil
}
