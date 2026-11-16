package state

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/flags"
)

// MemStore keeps experiments for apid tests; it has no request telemetry to
// read exposures from, so AdvanceFlagExperimentUnits reports that.

type memFlagExperimentUnit struct {
	variant   string
	converted bool
	crossed   bool
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
	x.ID, x.Status, x.MetricKind, x.CreatedAt = uuid.NewString(), flags.ExperimentDraft, flags.ExperimentMetricConversion, time.Now().UTC()
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
func (m *MemStore) StartFlagExperiment(_ context.Context, environmentID, key string, weights map[string]int, now time.Time, maxRunning int) (FlagExperiment, error) {
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
	x.Status, x.StartedAt, x.TelemetryCursor, x.Weights = flags.ExperimentRunning, &at, at, weights
	return cloneFlagExperiment(x), nil
}

// StopFlagExperiment implements FlagExperimentStore.
func (m *MemStore) StopFlagExperiment(_ context.Context, environmentID, key string, now time.Time) (FlagExperiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.memExperiment(environmentID, key)
	if x == nil || x.Status != flags.ExperimentRunning {
		return FlagExperiment{}, ErrNotFound
	}
	at := now.UTC()
	x.Status, x.StoppedAt = flags.ExperimentStopped, &at
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
func (m *MemStore) FlagExperimentArms(_ context.Context, experimentID string) ([]flags.Arm, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	byVariant := map[string]*flags.Arm{}
	var crossed int64
	for _, u := range m.flagEvents.units[experimentID] {
		if u.crossed {
			crossed++
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

// AdvanceFlagExperimentUnits reads exposures from request telemetry, which
// MemStore does not keep (ADR-127): like the other telemetry readers it
// returns errMemStoreRequestTelemetry, and unit tracking is tested against
// Postgres (TestPg_FlagExperimentUnitsFromTelemetry).
func (m *MemStore) AdvanceFlagExperimentUnits(context.Context, FlagExperiment, time.Time, time.Time, int) (int, error) {
	return 0, errMemStoreRequestTelemetry
}

// SaveFlagExperimentProgress implements FlagExperimentStore.
func (m *MemStore) SaveFlagExperimentProgress(_ context.Context, experimentID string, cursor time.Time, minPValues map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x := m.flagEventsLocked().experimentsLocked()[experimentID]; x != nil && x.Status == flags.ExperimentRunning {
		x.TelemetryCursor, x.MinPValues = cursor, minPValues
	}
	return nil
}
