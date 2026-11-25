package dbsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var upgradeRehearsalNamespace = uuid.MustParse("0017041d-2974-4e1f-85a6-2406430cb763")

// UpgradeRehearsal holds the upgrade plan and the last phase of a disposable drill.
type UpgradeRehearsal struct {
	SourceMajor, TargetMajor int
	RestoreStarted           bool
	RecoveryTimeline         uint32
	DatabaseName, Phase      string
}

func (s *Scheduler) RequestUpgradeRehearsal(ctx context.Context, sourceID, requestID string, major int, database string) (Drill, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return Drill{}, ErrConflict
	}
	sourceKey, sourceErr := uuid.Parse(sourceID)
	requestKey, requestErr := uuid.Parse(requestID)
	if sourceErr != nil || requestErr != nil || requestKey == uuid.Nil || major < dbguest.MinMajor || major > dbguest.MaxMajor || database == "" {
		return Drill{}, ErrInvalid
	}
	sourceID = sourceKey.String()
	id := uuid.NewSHA1(upgradeRehearsalNamespace, append(sourceKey[:], requestKey[:]...)).String()
	read := func() (Drill, error) {
		d, err := s.GetUpgradeRehearsal(ctx, sourceID, id)
		if err == nil && d.Rehearsal.TargetMajor != major {
			return Drill{}, fmt.Errorf("%w: request ID already targets PostgreSQL %d", ErrConflict, d.Rehearsal.TargetMajor)
		}
		return d, err
	}
	if d, err := read(); !errors.Is(err, ErrNotFound) {
		return d, err
	}
	source, err := store.Get(ctx, sourceID)
	if err != nil {
		return Drill{}, err
	}
	if err := s.drillSourceReady(source); err != nil {
		return Drill{}, err
	}
	if source.Major < dbguest.MinMajor {
		return Drill{}, fmt.Errorf("%w: PostgreSQL %d is unavailable for rehearsals", ErrInvalid, source.Major)
	}
	if err := s.upgradable(ctx, source, major); err != nil {
		return Drill{}, err
	}
	err = store.q.QueueDatabaseUpgradeRehearsal(ctx, store.pool, sqlc.QueueDatabaseUpgradeRehearsalParams{
		DrillID: mustUUID(id), SourceID: mustUUID(sourceID), NodeID: source.NodeID,
		RequestID: mustUUID(requestKey.String()), SourceMajor: int32(source.Major), TargetMajor: int32(major),
		DatabaseName: database, TargetAt: pgtype.Timestamptz{Time: time.Time{}, Valid: true},
	})
	if err != nil {
		return Drill{}, err
	}
	d, err := read()
	if errors.Is(err, ErrNotFound) {
		return Drill{}, fmt.Errorf("%w: a database drill or rehearsal is already queued or running", ErrConflict)
	}
	return d, err
}

func (s *Scheduler) GetUpgradeRehearsal(ctx context.Context, sourceID, id string) (Drill, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return Drill{}, ErrConflict
	}
	if _, err := uuid.Parse(id); err != nil {
		return Drill{}, ErrInvalid
	}
	d, err := store.requestedDrill(ctx, sourceID, id)
	if err == nil && d.Rehearsal == nil {
		return Drill{}, ErrNotFound
	}
	return d, err
}

func (p *PgStore) withUpgradeRehearsal(ctx context.Context, d Drill) (Drill, error) {
	r, err := p.q.GetDatabaseUpgradeRehearsal(ctx, p.pool, mustUUID(d.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	if err == nil {
		d.RequestID = uuid.UUID(r.RequestID.Bytes).String()
		d.Rehearsal = &UpgradeRehearsal{RestoreStarted: r.RestoreStarted, RecoveryTimeline: uint32(r.RecoveryTimeline), SourceMajor: int(r.SourceMajor), TargetMajor: int(r.TargetMajor), DatabaseName: r.DatabaseName, Phase: r.Phase}
	}
	return d, err
}

func (p *PgStore) isUpgradeRehearsalRestore(ctx context.Context, vm VM) (bool, error) {
	return p.q.IsDatabaseUpgradeRehearsalRestore(ctx, p.pool, sqlc.IsDatabaseUpgradeRehearsalRestoreParams{
		DrillID: mustUUID(vm.DatabaseID), SourceID: mustUUID(vm.RestoreSource),
		TargetAt: pgtype.Timestamptz{Time: vm.RestoreTarget, Valid: true},
	})
}

func (s *Scheduler) advanceUpgradeRehearsal(ctx context.Context, store *PgStore, d Drill) error {
	if !d.Rehearsal.RestoreStarted {
		if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout {
			return s.failUpgradeRehearsal(ctx, store, d, "restore_timeout", time.Time{})
		}
		return s.startRehearsalRestore(ctx, store, d)
	}

	vm, err := store.Get(ctx, d.ID)
	if errors.Is(err, ErrNotFound) || err == nil && !ownsRehearsalCopy(d, vm) {
		return s.failUpgradeRehearsal(ctx, store, d, "rehearsal_copy_unavailable", time.Time{})
	}
	if err != nil {
		return err
	}
	if vm.NodeID != d.NodeID {
		return s.failUpgradeRehearsal(ctx, store, d, "rehearsal_node_changed", vm.RestoreRecoveredTo)
	}
	setPhase := func(phase string) error {
		return store.q.SetDatabaseUpgradeRehearsalPhase(ctx, store.pool, sqlc.SetDatabaseUpgradeRehearsalPhaseParams{
			DrillID: mustUUID(d.ID), Phase: phase,
		})
	}
	failed := func(code string) error { return s.failUpgradeRehearsal(ctx, store, d, code, vm.RestoreRecoveredTo) }
	switch d.Rehearsal.Phase {
	case "restore":
		if vm.State == StateFailed {
			return failed(errCodeRestore)
		}
		if vm.Bootstrapped {
			return setPhase("mark")
		}
		if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout {
			return failed("restore_timeout")
		}
		s.resumeRestore(ctx, vm)
	case "mark":
		if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout {
			return failed("mark_timeout")
		}
		if _, err := s.markDrillOn(ctx, d, d.ID); err != nil {
			if errors.Is(err, errDrillMark) || errors.Is(err, errDrillSourceOutdated) {
				return failed(dbguest.ErrCodeDrillMarkFailed)
			}
			return err
		}
		// Persist this before invoking pg_upgrade so no retry can write the
		// mark on a copy that has already booted with the new major.
		return setPhase("upgrade")
	case "upgrade":
		if vm.Upgrade.Operation == d.ID {
			switch vm.Upgrade.Result {
			case UpgradeOK:
				return setPhase("verify")
			case UpgradeRefused:
				return failed(dbguest.ErrCodeUpgradeRefused)
			case UpgradeFailed:
				return failed(dbguest.ErrCodeUpgradeFailed)
			}
			// The normal upgrade worker owns its lease and deadline.
			if vm.State == StateUpgrading {
				return nil
			}
			return failed(dbguest.ErrCodeUpgradeFailed)
		}
		if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout {
			return failed("upgrade_start_timeout")
		}
		_, err := s.StartUpgradeChecked(ctx, d.ID, d.Rehearsal.TargetMajor, d.ID, d.Rehearsal.DatabaseName)
		switch {
		case errors.Is(err, ErrUpgradeExtensions):
			return failed("upgrade_extensions_unavailable")
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrConflict):
			return failed("upgrade_preflight_failed")
		}
		return err
	case "verify":
		if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout+s.cfg.UpgradeTimeout {
			return failed("verification_timeout")
		}
		if vm.Major != d.Rehearsal.TargetMajor || vm.Upgrade.Operation != d.ID || vm.Upgrade.Result != UpgradeOK {
			return failed("upgrade_version_mismatch")
		}
		resp, err := s.readDrillMark(ctx, d)
		if err != nil {
			return err
		}
		if resp.Major != d.Rehearsal.TargetMajor {
			return failed("upgrade_version_mismatch")
		}
		if resp.Error == dbguest.ErrCodeDrillMarkMissing {
			return failed(resp.Error)
		}
		if resp.Error != "" || resp.State != dbguest.StateReady {
			return failed(dbguest.ErrCodeDrillMarkFailed)
		}
		return s.completeRequestedDrill(ctx, store, d, DrillOK, "", vm.RestoreRecoveredTo)
	}
	return nil
}

func (s *Scheduler) startRehearsalRestore(ctx context.Context, store *PgStore, d Drill) error {
	failed := func(code string) error { return s.failUpgradeRehearsal(ctx, store, d, code, time.Time{}) }
	// Recover the handoff if the copy was placed before the result update.
	if !d.TargetAt.IsZero() {
		vm, err := store.Get(ctx, d.ID)
		if err == nil {
			if !ownsRehearsalCopy(d, vm) || vm.NodeID != d.NodeID {
				return failed("rehearsal_copy_unavailable")
			}
			if _, err := s.resumeExisting(ctx, vm, d.SourceID, d.TargetAt); err != nil {
				return err
			}
			return store.q.MarkDatabaseUpgradeRehearsalRestoring(ctx, store.pool, mustUUID(d.ID))
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	busy, err := store.otherActiveDrill(ctx, d, false)
	if err != nil || busy {
		return err
	}
	source, err := store.Get(ctx, d.SourceID)
	if errors.Is(err, ErrNotFound) || err == nil && (s.drillSourceReady(source) != nil || source.NodeID != d.NodeID || source.Major != d.Rehearsal.SourceMajor) {
		return failed("rehearsal_source_changed")
	}
	if err != nil {
		return err
	}
	if d.TargetAt.IsZero() {
		checker, ok := s.vmm.(interface {
			CheckLatestArchiveRestore(context.Context, Node, string, time.Time, int) (dbarchive.RestoreCheck, error)
		})
		if !ok {
			return failed("archive_check_unavailable")
		}
		node, err := s.node(ctx, d.NodeID)
		if errors.Is(err, ErrNodeUnavailable) || err == nil && node.Lifecycle != NodeActive {
			return failed("rehearsal_node_unavailable")
		}
		if err != nil {
			return err
		}
		ceiling := s.now().UTC().Truncate(time.Microsecond)
		check, err := checker.CheckLatestArchiveRestore(ctx, node, d.SourceID, ceiling, source.Major)
		if err != nil {
			return err
		}
		if check.Reason == "archive_busy" || check.Reason == "archive_unavailable" {
			return nil
		}
		if check.Outcome != dbarchive.RestoreCovered || check.RecoveryPoint == nil {
			code := check.Reason
			if !drillErrorCode.MatchString(code) {
				code = "no_verified_recovery_point"
			}
			return failed(code)
		}
		d.TargetAt = check.RecoveryPoint.UTC().Truncate(time.Microsecond)
		req := drillRestoreRequest(d, source)
		if req.Major != d.Rehearsal.SourceMajor || s.restorable(source, req) != nil {
			return failed("no_verified_recovery_point")
		}
		if check.Timeline > 1 {
			d.Rehearsal.RecoveryTimeline = check.Timeline
		}
		err = store.q.SetDatabaseUpgradeRehearsalTarget(ctx, store.pool, sqlc.SetDatabaseUpgradeRehearsalTargetParams{
			DrillID: mustUUID(d.ID), TargetAt: pgtype.Timestamptz{Time: d.TargetAt, Valid: true}, RecoveryTimeline: int64(d.Rehearsal.RecoveryTimeline),
		})
		if err != nil {
			return err
		}
	}
	req := drillRestoreRequest(d, source)
	req.restoreOnNode, req.restoreTimeline = d.NodeID, d.Rehearsal.RecoveryTimeline
	if _, err := s.Restore(ctx, req); err != nil {
		switch {
		case errors.Is(err, ErrNoCapacity):
			return failed("no_capacity")
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrConflict), errors.Is(err, ErrNodeUnavailable):
			return failed(errCodeRestore)
		default:
			return err
		}
	}
	return store.q.MarkDatabaseUpgradeRehearsalRestoring(ctx, store.pool, mustUUID(d.ID))
}

func (s *Scheduler) failUpgradeRehearsal(ctx context.Context, store *PgStore, d Drill, code string, recovered time.Time) error {
	return s.completeRequestedDrill(ctx, store, d, DrillFailed, code, recovered)
}

func ownsRehearsalCopy(d Drill, vm VM) bool {
	return vm.RestoreSource == d.SourceID && vm.RestoreTarget.Equal(d.TargetAt) && !vm.RestoreFromRecovery
}

func (s *Scheduler) discardUpgradeRehearsal(ctx context.Context, store *PgStore, d Drill) error {
	vm, err := store.Get(ctx, d.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		// Delete may already have removed the VM before archive cleanup.
	case err != nil:
		return err
	case !ownsRehearsalCopy(d, vm):
		return nil
	case vm.State == stateRehearsalRestore:
		s.stopRestore(d.ID)
		row, err := store.q.GetDatabaseVM(ctx, store.pool, mustUUID(d.ID))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if row.State == string(stateRehearsalRestore) && row.RestoreLeaseUntil.Time.After(s.now()) {
			return fmt.Errorf("%w: a restore worker still holds the copy lease", ErrConflict)
		}
	}
	if err := s.Delete(ctx, d.ID); err != nil {
		return err
	}
	node, err := s.node(ctx, d.NodeID)
	switch {
	case errors.Is(err, ErrNodeUnavailable), err == nil && node.Lifecycle == NodeUnavailable:
		return s.dropLostArchive(ctx, d.ID)
	case err != nil:
		return err
	default:
		return s.vmm.DropArchive(ctx, node, d.ID)
	}
}
