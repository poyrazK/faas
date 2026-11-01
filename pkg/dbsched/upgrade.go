package dbsched

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// In-place major upgrades (ADR-1027). An upgrade boots the database's
// volume on the new major's base with the old major's base beside it, and
// guest-init runs pg_upgrade --link (guest/init/database_upgrade.go). The
// database is parked for it, so clients see one stop and a boot that lasts
// as long as pg_upgrade. The record stays upgrading under a lease the
// running replica renews, and any replica resumes an upgrade whose lease
// lapsed by booting it again: guest-init carries on from wherever the
// volume is, and a VM still running from the last attempt is left to
// finish, since stopping pg_upgrade while it links files breaks the
// cluster.
//
// Once the new major serves, the scheduler takes its first base backup
// there, before any client can connect, and records that the database ran
// the old major until then: a point-in-time restore to an earlier instant
// recovers on the old major (restore.go). The database then parks, or runs
// on when it was in use or is always-on.
//
// pg_upgrade's check refusing the cluster leaves it intact on its old
// major: the database parks with last_error upgrade_refused. An upgrade
// that broke the cluster after pg_upgrade began linking its files fails
// the database (upgrade_failed); it needs a point-in-time restore. Any
// other failure (an unreachable node, a VM that did not boot) is retried
// until UpgradeTimeout after the upgrade began, then counted failed.

var (
	// ErrUpgradeRefused: pg_upgrade refused the cluster, which stays on
	// its major; the VM console says why.
	ErrUpgradeRefused = errors.New("dbsched: PostgreSQL refused the upgrade; the database stays on its major")
	// ErrUpgradeFailed: the upgrade broke the cluster; the database needs
	// a point-in-time restore, which recovers on its old major.
	ErrUpgradeFailed = errors.New("dbsched: the upgrade failed; restore the database to a point before it")
)

// upgradePoll is how often an upgrade polls its guest and Upgrade its
// record.
const upgradePoll = 200 * time.Millisecond

// upgradeRun is one upgrade this process runs.
type upgradeRun struct {
	done chan struct{}
}

// Upgrade upgrades a database in place to major as operation and returns
// it once the upgrade ended: ErrUpgradeRefused or ErrUpgradeFailed when it
// did not succeed. ctx bounds only the wait; the upgrade runs on in the
// background and Get reports it upgrading until it ends.
func (s *Scheduler) Upgrade(ctx context.Context, id string, major int, operation string) (VM, error) {
	if _, err := s.StartUpgrade(ctx, id, major, operation); err != nil {
		return VM{}, err
	}
	if run, ok := s.upgrades.Load(id); ok {
		select {
		case <-run.(*upgradeRun).done:
		case <-ctx.Done():
			return VM{}, ctx.Err()
		}
	}
	for {
		vm, err := s.Get(ctx, id)
		if err != nil || vm.State != StateUpgrading {
			return vm, errors.Join(err, upgradeOutcome(vm, operation))
		}
		select {
		case <-ctx.Done():
			return vm, ctx.Err()
		case <-time.After(upgradePoll):
		}
	}
}

// upgradeOutcome reads an ended upgrade's outcome from its record.
func upgradeOutcome(vm VM, operation string) error {
	if vm.DatabaseID == "" {
		return nil
	}
	switch {
	case vm.Upgrade.Operation != operation:
		return fmt.Errorf("%w: another upgrade replaced %s", ErrConflict, operation)
	case vm.Upgrade.Result == UpgradeRefused:
		return ErrUpgradeRefused
	case vm.Upgrade.Result == UpgradeFailed:
		return ErrUpgradeFailed
	}
	return nil
}

// StartUpgrade begins an in-place upgrade of a database to major, named
// operation (a lowercase UUID: apid's journaled request), and returns it
// upgrading once that is durable; the upgrade runs in the background. A
// running database is parked first. Starting the same operation again
// returns the database with the upgrade's progress or outcome in
// VM.Upgrade, and resumes it if its lease lapsed; it never upgrades twice.
func (s *Scheduler) StartUpgrade(ctx context.Context, id string, major int, operation string) (VM, error) {
	if major < dbguest.MinMajor || major > dbguest.MaxMajor {
		return VM{}, fmt.Errorf("%w: unsupported postgres major %d", ErrInvalid, major)
	}
	if parsed, err := uuid.Parse(operation); err != nil || parsed.String() != operation {
		return VM{}, fmt.Errorf("%w: the upgrade's operation is a lowercase UUID", ErrInvalid)
	}
	vm, held, err := s.beginUpgrade(ctx, id, major, operation)
	if err != nil {
		return vm, err
	}
	if vm.State == StateUpgrading {
		s.resumeUpgrade(ctx, id, held)
	}
	return s.withTarget(ctx, vm), nil
}

// beginUpgrade parks the database if it runs and marks it upgrading,
// returning the lease it took (zero: the operation was already recorded).
// A failure leaves the database as it was.
func (s *Scheduler) beginUpgrade(ctx context.Context, id string, major int, operation string) (VM, time.Time, error) {
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return vm, time.Time{}, err
	}
	switch {
	case vm.Upgrade.Operation == operation && vm.Upgrade.To != major:
		return vm, time.Time{}, fmt.Errorf("%w: operation %s upgraded to PostgreSQL %d", ErrConflict, operation, vm.Upgrade.To)
	case vm.Upgrade.Operation == operation:
		return vm, time.Time{}, nil
	case vm.State == StateUpgrading:
		return vm, time.Time{}, fmt.Errorf("%w: the database is upgrading to PostgreSQL %d", ErrConflict, vm.Upgrade.To)
	}
	if err := s.upgradable(ctx, vm, major); err != nil {
		return vm, time.Time{}, err
	}
	wake := vm.State == StateRunning
	if wake {
		if err := s.parkLocked(ctx, id); err != nil {
			return vm, time.Time{}, err
		}
	}
	upgrading, err := s.markUpgrading(ctx, vm, major, operation)
	if err != nil {
		if wake {
			_, _ = s.wakeLocked(context.WithoutCancel(ctx), id) // back up where it was
		}
		return vm, time.Time{}, err
	}
	s.log.Info("dbsched: upgrade started", "database_id", id, "node_id", vm.NodeID, "from", vm.Major, "to", major)
	return upgrading, upgrading.UpgradeLeaseUntil, nil
}

// upgradable checks an upgrade of vm to major before it parks anything.
func (s *Scheduler) upgradable(ctx context.Context, vm VM, major int) error {
	node, err := s.node(ctx, vm.NodeID)
	switch {
	case err != nil:
		return err
	case vm.State != StateParked && vm.State != StateRunning:
		return fmt.Errorf("%w: the database is %s", ErrConflict, vm.State)
	case !vm.Bootstrapped || vm.ResizePending || vm.PromotePending:
		return fmt.Errorf("%w: the database has a resize, promotion or first boot pending; wake it first", ErrConflict)
	case major <= vm.Major:
		return fmt.Errorf("%w: the database runs PostgreSQL %d already", ErrInvalid, vm.Major)
	case !node.offers(major):
		return fmt.Errorf("%w: node %s has no PostgreSQL %d base", ErrInvalid, node.ID, major)
	case !node.offers(vm.Major):
		return fmt.Errorf("%w: node %s has no PostgreSQL %d base, which pg_upgrade runs", ErrInvalid, node.ID, vm.Major)
	case node.Lifecycle != NodeActive:
		return fmt.Errorf("%w: node %s is %s", ErrConflict, node.ID, node.Lifecycle)
	}
	return nil
}

// markUpgrading records the parked database upgrading. Its standby stays
// on the old major, so it must not take over first.
func (s *Scheduler) markUpgrading(ctx context.Context, vm VM, major int, operation string) (VM, error) {
	if vm.Replicated {
		if err := s.store.MarkStandbySynced(ctx, vm.DatabaseID, false); err != nil {
			return vm, err
		}
	}
	return s.store.BeginUpgrade(ctx, vm.DatabaseID, major, operation, s.cfg.UpgradeLease)
}

// ResumeUpgrades takes over upgrades whose lease lapsed: their replica
// stopped, or their last attempt failed on something passing.
func (s *Scheduler) ResumeUpgrades(ctx context.Context) error {
	vms, err := s.store.List(ctx, StateUpgrading)
	if err != nil {
		return err
	}
	for _, vm := range vms {
		if vm.UpgradeLeaseUntil.Before(s.now()) {
			s.resumeUpgrade(ctx, vm.DatabaseID, time.Time{})
		}
	}
	return nil
}

// resumeUpgrade runs id's upgrade in the background unless this process
// already does: under held, the lease the caller took, or zero to take one
// that lapsed.
func (s *Scheduler) resumeUpgrade(ctx context.Context, id string, held time.Time) {
	run := &upgradeRun{done: make(chan struct{})}
	if _, running := s.upgrades.LoadOrStore(id, run); running {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.UpgradeTimeout)
	s.upgradeWG.Add(1)
	go func() {
		defer s.upgradeWG.Done()
		defer close(run.done)
		defer s.upgrades.Delete(id)
		defer cancel()
		s.runUpgrade(ctx, id, held)
	}()
}

// runUpgrade runs one attempt at an upgrade under its lease.
func (s *Scheduler) runUpgrade(ctx context.Context, id string, held time.Time) {
	held, err := s.store.ClaimUpgrade(ctx, id, held, s.cfg.UpgradeLease)
	if err != nil {
		return // another replica holds it, or it ended
	}
	lease := &heldLease{held: held}
	attempt, cancel := context.WithCancel(ctx)
	var lost atomic.Bool
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		keepLease(attempt, lease, s.cfg.UpgradeLease, func(ctx context.Context, held time.Time) (time.Time, error) {
			return s.store.ClaimUpgrade(ctx, id, held, s.cfg.UpgradeLease)
		}, func(err error) {
			s.log.Warn("dbsched: upgrade lease lost", "database_id", id, "err", err)
			lost.Store(true)
			cancel()
		})
	}()
	defer func() { cancel(); <-renewed }()
	vm, err := s.store.Get(attempt, id)
	if err != nil {
		return
	}
	node, err := s.node(attempt, vm.NodeID)
	if s.now().Sub(vm.StateChangedAt) > s.cfg.UpgradeTimeout {
		cause := errors.Join(fmt.Errorf("dbsched: upgrade of %s gave up after %v", id, s.cfg.UpgradeTimeout), err)
		s.endUpgrade(context.WithoutCancel(ctx), node, vm, lease.get(), StateFailed, dbguest.ErrCodeUpgradeFailed, cause)
		return
	}
	var resp dbguest.Response
	if err == nil {
		resp, err = s.bootUpgrade(attempt, node, vm)
	}
	if err == nil && resp.State == dbguest.StateReady {
		err = s.finishUpgrade(attempt, node, vm, lease)
	}
	if lost.Load() {
		return // whoever holds the lease now carries on
	}
	settle := context.WithoutCancel(ctx)
	switch {
	case err != nil:
		// Retried once the lease lapses.
		s.log.Warn("dbsched: upgrade attempt failed; it resumes", "database_id", id, "err", err)
	case resp.State == dbguest.StateReady:
	case resp.Error == dbguest.ErrCodeUpgradeRefused:
		s.endUpgrade(settle, node, vm, lease.get(), StateParked, dbguest.ErrCodeUpgradeRefused, ErrUpgradeRefused)
	case resp.Error == dbguest.ErrCodeUpgradeFailed:
		s.endUpgrade(settle, node, vm, lease.get(), StateFailed, dbguest.ErrCodeUpgradeFailed, ErrUpgradeFailed)
	default:
		// The guest failed otherwise: before pg_upgrade, or starting the
		// upgraded cluster. Its next boot carries on from the volume.
		_ = s.vmm.Stop(settle, node, id)
		s.log.Warn("dbsched: upgrade boot failed; it resumes", "database_id", id, "err", guestError(resp))
	}
}

// bootUpgrade boots the upgrade, or finds the last attempt's VM still at
// it, and waits until the guest serves the new major or fails.
func (s *Scheduler) bootUpgrade(ctx context.Context, node Node, vm VM) (dbguest.Response, error) {
	boot := vm
	boot.Major, boot.UpgradeFrom = vm.Upgrade.To, vm.Major
	// A VM still running from an earlier attempt counts as started.
	if err := s.vmm.Start(ctx, node, boot); err != nil {
		return dbguest.Response{}, fmt.Errorf("boot: %w", err)
	}
	for {
		resp, err := s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
		switch {
		case err == nil && (resp.State == dbguest.StateReady || resp.State == dbguest.StateFailed):
			return resp, nil
		case errors.Is(err, ErrVMNotRunning):
			return resp, err
		}
		select {
		case <-ctx.Done():
			return resp, fmt.Errorf("wait for the upgrade: %w", errors.Join(ctx.Err(), err))
		case <-time.After(upgradePoll):
		}
	}
}

// finishUpgrade backs up the upgraded database, then parks it or leaves it
// running, and records the new major.
func (s *Scheduler) finishUpgrade(ctx context.Context, node Node, vm VM, lease *heldLease) error {
	id := vm.DatabaseID
	backupAt, ok := s.baseBackup(ctx, node, id)
	if !ok {
		s.log.Warn("dbsched: no base backup on the new major yet; restores to after the upgrade wait for the backup sweep's",
			"database_id", id)
	}
	until := s.now()
	to := StateParked
	if vm.AlwaysOn || until.Sub(vm.LastActiveAt) < s.cfg.IdleTimeout {
		to = StateRunning
	} else if err := s.vmm.Stop(ctx, node, id); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := s.store.FinishUpgrade(ctx, id, lease.get(), to, until, backupAt); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	s.cfg.Metrics.upgraded(UpgradeOK)
	s.log.Info("dbsched: upgraded", "database_id", id, "from", vm.Major, "to", vm.Upgrade.To, "took", s.now().Sub(vm.StateChangedAt),
		"backed_up", ok, "state", to)
	switch {
	case to == StateParked:
		s.freed.signal(vm.NodeID)
	case vm.Replicated:
		s.kickStandby(context.WithoutCancel(ctx), id)
	}
	return nil
}

// endUpgrade stops the upgrade's VM and ends the upgrade on the old major,
// in to.
func (s *Scheduler) endUpgrade(ctx context.Context, node Node, vm VM, held time.Time, to State, code string, cause error) {
	if node.ID != "" {
		if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
			s.log.Warn("dbsched: stop an ended upgrade's VM", "database_id", vm.DatabaseID, "err", err)
		}
	}
	if err := s.store.AbortUpgrade(ctx, vm.DatabaseID, held, to, code); err != nil {
		s.log.Warn("dbsched: end upgrade", "database_id", vm.DatabaseID, "err", err)
		return
	}
	s.freed.signal(vm.NodeID)
	result := upgradeResultFor(to)
	s.cfg.Metrics.upgraded(result)
	s.log.Warn("dbsched: upgrade ended", "database_id", vm.DatabaseID, "from", vm.Major, "to", vm.Upgrade.To, "result", result, "err", cause)
}
