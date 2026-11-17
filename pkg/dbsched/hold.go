package dbsched

import (
	"context"
	"fmt"
	"time"
)

// Compute holds and quotas (ADR-1027 amendment 2026-11-10). apid holds a
// database in its delete grace period and when its account's compute cap
// suspends it (ADR-20261025094915), and gives each database a monthly compute
// quota, the cap's backstop between sweeps. A held database, or one whose
// compute this UTC month reached its quota, is parked and its wakes are
// refused: pgproxyd tells its clients why. Nothing is deleted. Releasing the
// hold, raising the quota or the next month lets the next connection wake it.
//
// The hold is checked on every wake, after the database's lock, so a hold
// stops this replica's wakes at once. The quota reads the usage ledger, so
// only a boot checks it. A wake another replica admitted just before the
// hold landed, and a running database that passes its quota, are parked by
// the next idle sweep (ParkHeld).

// SetHold holds or releases a database. Holding a running database parks
// it; one that is provisioning, moving or upgrading is parked by the idle
// sweep once it runs.
func (s *Scheduler) SetHold(ctx context.Context, id string, held bool) (VM, error) {
	unlock := s.lock(id)
	defer unlock()
	if err := s.store.SetHeld(ctx, id, held); err != nil {
		return VM{}, err
	}
	if held {
		if err := s.parkRefused(ctx, id); err != nil {
			return VM{}, err
		}
	}
	return s.Get(ctx, id)
}

// SetComputeQuota sets a database's monthly compute quota in compute-unit
// seconds; zero clears it. A running database already past the new quota
// is parked.
func (s *Scheduler) SetComputeQuota(ctx context.Context, id string, quotaUnitSeconds int64) (VM, error) {
	if quotaUnitSeconds < 0 {
		return VM{}, fmt.Errorf("dbsched: compute quota %d: %w", quotaUnitSeconds, ErrInvalid)
	}
	unlock := s.lock(id)
	defer unlock()
	if err := s.store.SetComputeQuota(ctx, id, quotaUnitSeconds); err != nil {
		return VM{}, err
	}
	if err := s.parkRefused(ctx, id); err != nil {
		return VM{}, err
	}
	return s.Get(ctx, id)
}

// ComputeThisMonth is the database's compute-unit seconds since the start
// of the UTC month, its open interval included, and that month's start.
func (s *Scheduler) ComputeThisMonth(ctx context.Context, id string) (int64, time.Time, error) {
	now := s.now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	usage, err := s.store.Usage(ctx, id, month, now)
	if err != nil {
		return 0, month, err
	}
	return usage.ComputeUnitSeconds, month, nil
}

// ParkHeld parks running databases that are held or past their quota. It
// reports how many it parked.
func (s *Scheduler) ParkHeld(ctx context.Context) (int, error) {
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return 0, err
	}
	parked := 0
	for _, vm := range running {
		if !vm.Held && vm.ComputeQuotaUnitSeconds == 0 {
			continue
		}
		if s.refusal(ctx, vm) == nil {
			continue
		}
		if err := s.Park(ctx, vm.DatabaseID); err != nil {
			s.log.Warn("dbsched: park a held database failed", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		parked++
	}
	return parked, nil
}

// parkRefused parks the database if it is running and refused. The caller
// holds its lock.
func (s *Scheduler) parkRefused(ctx context.Context, id string) error {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if vm.State != StateRunning || s.refusal(ctx, vm) == nil {
		return nil
	}
	return s.parkLocked(ctx, id)
}

// refusal is why the database may not run, or nil. A quota whose use
// cannot be read refuses nothing: the hold, not the quota, is what stops a
// database, and the next sweep reads again.
func (s *Scheduler) refusal(ctx context.Context, vm VM) error {
	if vm.Held {
		return ErrHeld
	}
	if vm.ComputeQuotaUnitSeconds <= 0 {
		return nil
	}
	used, _, err := s.ComputeThisMonth(ctx, vm.DatabaseID)
	if err != nil {
		s.log.Warn("dbsched: read compute against a quota", "database_id", vm.DatabaseID, "err", err)
		return nil
	}
	if used >= vm.ComputeQuotaUnitSeconds {
		return ErrComputeQuota
	}
	return nil
}
