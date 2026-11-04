// adr: 20261028103115 — production provisioning: fleet database cap and
// monthly provider-spend breaker.
package managedpostgres

import (
	"context"
	"fmt"
	"time"
)

// FleetPolicy bounds the platform's managed PostgreSQL fleet across every
// account: how many databases exist and how much provider cost the usage
// ledger has recorded in the current UTC month. Zero leaves a ceiling unset.
type FleetPolicy struct {
	MaxDatabases             int
	MaxMonthlyCostMillicents int64
}

// Complete reports whether both ceilings are set, as production requires.
func (p FleetPolicy) Complete() bool {
	return p.MaxDatabases > 0 && p.MaxMonthlyCostMillicents > 0
}

// FleetTotals is the fleet's current size and month-to-date provider cost.
type FleetTotals struct {
	Databases      int
	CostMillicents int64
}

// FleetStore reports fleet-wide totals. Databases counts every resource the
// reservation limits count: undeleted databases and pending clone restores.
type FleetStore interface {
	FleetTotals(ctx context.Context, periodStart time.Time) (FleetTotals, error)
}

// ErrFleetPaused refuses new databases (and cost-raising work) while the
// fleet is at its database cap or its monthly provider-spend breaker is open.
// It wraps ErrUnavailable: nothing is down and a later retry may succeed.
var ErrFleetPaused = fmt.Errorf("%w: new managed databases are paused platform-wide", ErrUnavailable)

// FleetPausedDetail is the customer-facing explanation for ErrFleetPaused.
// It names no fleet sizes or costs.
const FleetPausedDetail = "New managed PostgreSQL databases, restores and compute increases are paused on this platform right now. Existing databases and bindings keep working; retry later or contact the platform operator."

// Fleet returns the operator's fleet policy.
func (r *Registry) Fleet() FleetPolicy {
	if r == nil {
		return FleetPolicy{}
	}
	return r.fleet
}

// FleetStatus reads the fleet totals for the current UTC month. It fails
// when the store cannot report them.
func (s *Service) FleetStatus(ctx context.Context) (FleetTotals, FleetPolicy, error) {
	if s == nil || s.registry == nil {
		return FleetTotals{}, FleetPolicy{}, ErrUnavailable
	}
	store, ok := s.store.(FleetStore)
	if !ok {
		return FleetTotals{}, s.registry.Fleet(), ErrUnavailable
	}
	totals, err := store.FleetTotals(ctx, monthStart(s.now()))
	if err != nil {
		return FleetTotals{}, s.registry.Fleet(), ErrUnavailable
	}
	return totals, s.registry.Fleet(), nil
}

// admitFleet applies the fleet policy before work that adds provider cost.
// The spend breaker refuses any such work; the database cap refuses only
// work that adds a database. Concurrent reservations can overshoot the cap
// by at most the number in flight: it is a safety ceiling, not a quota.
func (s *Service) admitFleet(ctx context.Context, addsDatabase bool) error {
	policy := s.registry.Fleet()
	if policy.MaxDatabases <= 0 && policy.MaxMonthlyCostMillicents <= 0 {
		return nil
	}
	totals, _, err := s.FleetStatus(ctx)
	if err != nil {
		return err
	}
	if policy.MaxMonthlyCostMillicents > 0 && totals.CostMillicents >= policy.MaxMonthlyCostMillicents {
		return ErrFleetPaused
	}
	if addsDatabase && policy.MaxDatabases > 0 && totals.Databases >= policy.MaxDatabases {
		return ErrFleetPaused
	}
	return nil
}
