package managedpostgres

import (
	"context"
	"errors"
	"time"
)

// DatabaseObserver must only read provider metadata. It must never connect to
// PostgreSQL, wake compute, repair permissions, or mutate provider resources.
// Inspect remains the lifecycle boundary, including restore security cleanup.
type DatabaseObserver interface {
	Observe(context.Context, string) (ObservedDatabase, error)
}

type HealthConfig struct {
	Enabled           *bool `json:"enabled,omitempty"`
	IntervalSeconds   int64 `json:"interval_seconds,omitempty"`
	StaleAfterSeconds int64 `json:"stale_after_seconds,omitempty"`
}

type HealthPolicy struct {
	Enabled    bool
	Interval   time.Duration
	StaleAfter time.Duration
}

func (c HealthConfig) policy() (HealthPolicy, error) {
	p := HealthPolicy{Enabled: c.Enabled == nil || *c.Enabled, Interval: time.Minute, StaleAfter: 5 * time.Minute}
	if c.IntervalSeconds != 0 {
		if c.IntervalSeconds < 60 || c.IntervalSeconds > 3600 {
			return HealthPolicy{}, ErrInvalid
		}
		p.Interval = time.Duration(c.IntervalSeconds) * time.Second
	}
	if c.StaleAfterSeconds != 0 {
		if c.StaleAfterSeconds < 120 || c.StaleAfterSeconds > 86400 {
			return HealthPolicy{}, ErrInvalid
		}
		p.StaleAfter = time.Duration(c.StaleAfterSeconds) * time.Second
	}
	if p.StaleAfter < 2*p.Interval {
		return HealthPolicy{}, ErrInvalid
	}
	return p, nil
}

type HealthSnapshot struct {
	ProviderStatus string
	ComputeState   ComputeState
	CheckedAt      time.Time
	LastSuccessAt  time.Time
	LastErrorCode  string
}

// HealthSummary describes provider metadata, not SQL reachability. Freshness
// applies to the latest attempt, including an observed provider/API failure.
type HealthSummary struct {
	Enabled           bool
	Status            string
	Fresh             bool
	StaleAfterSeconds int64
	HealthSnapshot
}

func (p HealthPolicy) Summarize(snapshot HealthSnapshot, now time.Time) HealthSummary {
	out := HealthSummary{Enabled: p.Enabled, Status: "unknown", StaleAfterSeconds: int64(p.StaleAfter / time.Second), HealthSnapshot: snapshot}
	if !p.Enabled {
		out.Status = "disabled"
		out.HealthSnapshot = HealthSnapshot{}
	}
	if out.ProviderStatus == "" {
		out.ProviderStatus = "unknown"
	}
	if out.ComputeState == "" {
		out.ComputeState = ComputeStateUnknown
	}
	if !p.Enabled || snapshot.CheckedAt.IsZero() {
		return out
	}
	out.Fresh = !snapshot.CheckedAt.After(now) && now.Sub(snapshot.CheckedAt) <= p.StaleAfter
	switch {
	case !out.Fresh:
		out.Status = "stale"
	case snapshot.LastErrorCode != "" || snapshot.ProviderStatus != string(ProviderStatusReady) || snapshot.ComputeState == ComputeStateUnknown:
		out.Status = "degraded"
	default:
		out.Status = "healthy"
	}
	return out
}

type HealthClaim struct {
	Database     Database
	LeaseToken   string
	LeaseUntil   time.Time
	AttemptCount int32
}

type HealthResult struct {
	HealthSnapshot
	Succeeded   bool
	NextCheckAt time.Time
}

type HealthCounts struct{ Healthy, Degraded, Unknown, Stale int64 }

type HealthStore interface {
	ClaimHealthCheck(context.Context, string, time.Time, time.Time) (HealthClaim, error)
	FinishHealthCheck(context.Context, HealthClaim, HealthResult) error
	ReadHealthSnapshots(context.Context, string, []string) (map[string]HealthSnapshot, error)
	CountDatabaseHealth(context.Context, time.Time, time.Time) (HealthCounts, error)
}

func (s *Service) withHealth(ctx context.Context, accountID string, databases []Database) ([]Database, error) {
	policy := s.registry.HealthPolicy()
	snapshots := map[string]HealthSnapshot{}
	if store, ok := s.store.(HealthStore); ok && policy.Enabled && len(databases) > 0 {
		ids := make([]string, len(databases))
		for i, database := range databases {
			ids[i] = database.ID
		}
		var err error
		snapshots, err = store.ReadHealthSnapshots(ctx, accountID, ids)
		if err != nil {
			return nil, err
		}
	}
	for i := range databases {
		health := policy.Summarize(snapshots[databases[i].ID], s.now())
		databases[i].Health = &health
	}
	return databases, nil
}

func healthErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "resource_missing"
	case errors.Is(err, ErrUnsupported):
		return "observer_unsupported"
	default:
		return "provider_unavailable"
	}
}

func validateHealthResult(result HealthResult) error {
	if result.CheckedAt.IsZero() || !result.NextCheckAt.After(result.CheckedAt) {
		return ErrInvalid
	}
	switch result.ProviderStatus {
	case "unknown", "missing", "pending", "ready", "deleting", "failed":
	default:
		return ErrInvalid
	}
	switch result.ComputeState {
	case ComputeStateUnknown, ComputeStateActive, ComputeStateSuspended, ComputeStateWaking:
	default:
		return ErrInvalid
	}
	switch result.LastErrorCode {
	case "", "resource_missing", "observer_unsupported", "provider_unavailable", "backend_unavailable", "observation_invalid", "spec_mismatch", "provider_failed":
	default:
		return ErrInvalid
	}
	return nil
}
