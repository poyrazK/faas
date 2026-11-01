package dbsched

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Restore drills (ADR-1027). Backups nobody restores are a hope, not a
// backup. Once a day per database node, the scheduler restores one of
// the node's databases into a throwaway database on the same node,
// through the same restore a customer's point-in-time restore runs: the
// source's archive flushed, its base backup extracted, its WAL replayed,
// the copy promoted and backed up in turn. First the source commits the
// drill's mark (its id, in a table only the guest's superuser reaches),
// and the drill recovers to just after that commit; once the copy has
// parked, the drill wakes it and looks for the mark, so a copy that came
// back without the source's last commits fails. The drill records the
// outcome and how far recovery got, then deletes the throwaway database
// and its archive. Customers never see a drill: its database is in no
// account's catalog. A drill database is never a source: its record stays
// running until the database is gone.

// DrillResult is a drill's outcome.
type DrillResult string

const (
	DrillRunning DrillResult = "running"
	DrillOK      DrillResult = "ok"
	DrillFailed  DrillResult = "failed"
	// DrillSkipped is a drill that could not start (no capacity on the
	// node); it is retried the next day, and is no failure of the backups.
	DrillSkipped DrillResult = "skipped"
)

// Drill is one restore drill. ID is the throwaway database's id.
type Drill struct {
	ID, NodeID, SourceID                         string
	TargetAt, StartedAt, FinishedAt, RecoveredTo time.Time
	Result                                       DrillResult
	ErrorCode                                    string
}

// DrillSummary is a node's drill history: its last drill's start and its
// last successful drill's end (zero: none).
type DrillSummary struct {
	LastStarted, LastOK time.Time
}

// DrillSample counts what one RunDrills pass did.
type DrillSample struct {
	Started, Running, OK, Failed, Skipped int
}

const (
	// drillMemMiB caps a drill's memory: recovery needs little, and the
	// node's admission charges it like any database.
	drillMemMiB = 384
	// drillRestoreWindowSeconds is the drill database's own restore
	// window, so dbbackupd may prune whatever its archive keeps.
	drillRestoreWindowSeconds = 3600
	// drillRetention keeps finished drills' records this long.
	drillRetention = 90 * 24 * time.Hour
	// maxDrillClockAhead is how far the source's clock may run ahead of
	// the scheduler's: a restore refuses a point in its future, so the
	// drill waits for its target to pass first.
	maxDrillClockAhead = 2 * time.Second
)

var (
	// errDrillMark: the source could not commit the drill's mark.
	errDrillMark = errors.New("dbsched: drill mark failed")
	// errDrillSourceOutdated: the source runs a guest without drill
	// marks, until its next boot.
	errDrillSourceOutdated = errors.New("dbsched: the drill's source runs an outdated guest")
)

// drillNamespace keys drill ids by source and day, so every schedd
// replica derives the same drill and only one starts it.
var drillNamespace = uuid.MustParse("6b0c9e64-3f1d-4c8a-9e57-2d4f1a6b8c30")

var drillErrorCode = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// RunDrills settles running drills and starts each active node's daily
// drill. The idle sweep calls it; DrillEvery < 0 turns drills off.
func (s *Scheduler) RunDrills(ctx context.Context) (DrillSample, error) {
	var sample DrillSample
	if s.cfg.DrillEvery < 0 {
		return sample, nil
	}
	running, err := s.store.RunningDrills(ctx)
	if err != nil {
		return sample, err
	}
	busy, drills := map[string]bool{}, map[string]bool{}
	for _, d := range running {
		if s.settleDrill(ctx, d, &sample) {
			continue
		}
		busy[d.NodeID], drills[d.ID] = true, true
		sample.Running++
	}
	if err := s.startDrills(ctx, busy, drills, &sample); err != nil {
		return sample, err
	}
	if _, err := s.store.PruneDrills(ctx, s.now().Add(-drillRetention)); err != nil {
		s.log.Warn("dbsched: prune restore drills", "err", err)
	}
	if summaries, err := s.store.DrillsByNode(ctx); err == nil {
		s.cfg.Metrics.observedDrills(summaries)
	}
	return sample, nil
}

// settleDrill ends a drill that has an outcome: it deletes the drill's
// database and archive and records the result. It reports whether the
// drill is over.
func (s *Scheduler) settleDrill(ctx context.Context, d Drill, sample *DrillSample) bool {
	result, code, recovered, done := s.drillOutcome(ctx, d)
	if !done {
		return false
	}
	if err := s.discardDrill(ctx, d); err != nil {
		// Retried on the next pass; the record stays running until then.
		s.log.Warn("dbsched: delete a restore drill's database", "drill_id", d.ID, "err", err)
		return false
	}
	if err := s.store.FinishDrill(ctx, d.ID, result, recovered, code); err != nil {
		s.log.Warn("dbsched: record a restore drill", "drill_id", d.ID, "err", err)
		return false
	}
	s.countDrill(result, sample)
	s.cfg.Metrics.drilled(result)
	if result == DrillOK {
		s.log.Info("dbsched: restore drill passed", "node", d.NodeID, "source", d.SourceID, "target", d.TargetAt,
			"recovered_to", recovered, "took", s.now().Sub(d.StartedAt).Round(time.Second))
	} else {
		s.log.Warn("dbsched: restore drill failed", "node", d.NodeID, "source", d.SourceID, "target", d.TargetAt, "error_code", code)
	}
	return true
}

// drillOutcome reads a running drill's database: done once the restore
// parked it and the copy was checked, once the restore failed, or once
// the drill has run past DrillTimeout.
func (s *Scheduler) drillOutcome(ctx context.Context, d Drill) (DrillResult, string, time.Time, bool) {
	vm, err := s.store.Get(ctx, d.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		return DrillFailed, "drill_vanished", time.Time{}, true
	case err != nil:
		return "", "", time.Time{}, false
	case (vm.State == StateParked || vm.State == StateRunning) && vm.Bootstrapped:
		// Running: an earlier pass woke it to check it.
		result, code, done := s.checkDrill(ctx, d)
		return result, code, vm.RestoreRecoveredTo, done
	case vm.State == StateFailed:
		code := vm.LastError
		if !drillErrorCode.MatchString(code) {
			code = errCodeRestore
		}
		return DrillFailed, code, vm.RestoreRecoveredTo, true
	case s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout:
		return DrillFailed, "drill_timeout", time.Time{}, true
	}
	return "", "", time.Time{}, false
}

// checkDrill wakes a restored drill database and looks for the drill's
// mark. A wake or check that gets no answer is retried on the next pass,
// until the drill times out.
func (s *Scheduler) checkDrill(ctx context.Context, d Drill) (DrillResult, string, bool) {
	resp, err := s.readDrillMark(ctx, d)
	switch {
	case err == nil && resp.Error == "":
		return DrillOK, "", true
	case err == nil && drillErrorCode.MatchString(resp.Error):
		return DrillFailed, resp.Error, true
	case err == nil:
		return DrillFailed, dbguest.ErrCodeDrillMarkFailed, true
	case s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout:
		return DrillFailed, "drill_timeout", true
	}
	s.log.Warn("dbsched: check a restore drill's database", "drill_id", d.ID, "err", err)
	return "", "", false
}

func (s *Scheduler) readDrillMark(ctx context.Context, d Drill) (dbguest.Response, error) {
	vm, err := s.Wake(ctx, d.ID)
	if err != nil {
		return dbguest.Response{}, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return dbguest.Response{}, err
	}
	return s.vmm.Control(ctx, node, d.ID, dbguest.Request{Op: dbguest.OpCheckDrill, Drill: d.ID})
}

// markDrill commits the drill's mark in its source, woken if parked, and
// returns the point the drill recovers to: the source's clock just after
// the commit, so a restore that replays the archive that far holds the
// mark. The restore's own flush then commits past it.
func (s *Scheduler) markDrill(ctx context.Context, d Drill) (time.Time, error) {
	source, err := s.Wake(ctx, d.SourceID)
	if err != nil {
		return time.Time{}, err
	}
	node, err := s.node(ctx, source.NodeID)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := s.vmm.Control(ctx, node, source.DatabaseID, dbguest.Request{Op: dbguest.OpMarkDrill, Drill: d.ID})
	_ = s.Touch(ctx, source.DatabaseID)
	switch {
	case err != nil:
		return time.Time{}, err
	case resp.Error == dbguest.ErrCodeInvalidRequest:
		return time.Time{}, errDrillSourceOutdated
	case resp.Error != "" || resp.MarkedAt.IsZero():
		return time.Time{}, fmt.Errorf("%w: %s", errDrillMark, cmp.Or(resp.Error, "no mark time"))
	}
	ahead := resp.MarkedAt.Sub(s.now())
	if ahead > maxDrillClockAhead {
		return time.Time{}, fmt.Errorf("%w: the source's clock runs %v ahead", errDrillMark, ahead)
	}
	if ahead > 0 {
		timer := time.NewTimer(ahead)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-timer.C:
		}
	}
	return resp.MarkedAt.UTC().Truncate(time.Microsecond), nil
}

// drillStartFailure is the outcome of a drill that did not start.
func drillStartFailure(err error) (DrillResult, string) {
	switch {
	case errors.Is(err, ErrNoCapacity):
		return DrillSkipped, "drill_no_capacity"
	case errors.Is(err, errDrillSourceOutdated):
		return DrillSkipped, "drill_source_outdated"
	case errors.Is(err, errDrillMark):
		return DrillFailed, dbguest.ErrCodeDrillMarkFailed
	}
	return DrillFailed, "drill_start_failed"
}

// discardDrill deletes a drill's database and the archive its restore
// wrote. Both are idempotent.
func (s *Scheduler) discardDrill(ctx context.Context, d Drill) error {
	if err := s.Delete(ctx, d.ID); err != nil {
		return err
	}
	// A lost node keeps the archive until it returns, and dbbackupd prunes
	// it there.
	if node, err := s.node(ctx, d.NodeID); err == nil {
		if err := s.vmm.DropArchive(ctx, node, d.ID); err != nil {
			s.log.Warn("dbsched: drop a restore drill's archive", "drill_id", d.ID, "err", err)
		}
	}
	return nil
}

// startDrills starts the daily drill of every active node that has a
// restorable database and no drill running.
func (s *Scheduler) startDrills(ctx context.Context, busy, drills map[string]bool, sample *DrillSample) error {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return err
	}
	summaries, err := s.store.DrillsByNode(ctx)
	if err != nil {
		return err
	}
	vms, err := s.store.List(ctx, StateParked, StateRunning)
	if err != nil {
		return err
	}
	now := s.now()
	day := now.UTC().Format(time.DateOnly)
	for _, node := range nodes {
		if node.Lifecycle != NodeActive || busy[node.ID] {
			continue
		}
		if last := summaries[node.ID].LastStarted; !last.IsZero() && now.Sub(last) < s.cfg.DrillEvery {
			continue
		}
		source, ok := pickDrillSource(vms, drills, node.ID, day, now)
		if !ok {
			continue
		}
		drill := Drill{ID: uuid.NewSHA1(drillNamespace, []byte(source.DatabaseID+"/"+day)).String(), NodeID: node.ID,
			SourceID: source.DatabaseID, TargetAt: now.UTC().Truncate(time.Microsecond)}
		// Every replica derives the same drill id, so a replica that loses
		// the race below rewrote the same mark.
		target, err := s.markDrill(ctx, drill)
		if err == nil {
			drill.TargetAt = target
		}
		started, serr := s.store.StartDrill(ctx, drill)
		if serr != nil {
			s.log.Warn("dbsched: record a restore drill", "node", node.ID, "err", serr)
			continue
		}
		if !started {
			// Another replica started it.
			continue
		}
		if err == nil {
			_, err = s.Restore(ctx, RestoreRequest{
				ProvisionRequest: ProvisionRequest{DatabaseID: drill.ID, Major: source.MajorAt(drill.TargetAt), VcpuCount: 1, MemMiB: min(source.MemMiB, drillMemMiB),
					VolumeBytes: source.VolumeBytes, RestoreWindowSeconds: drillRestoreWindowSeconds},
				Source: source.DatabaseID, Target: drill.TargetAt,
			})
		}
		if err == nil {
			sample.Started++
			s.log.Info("dbsched: restore drill started", "node", node.ID, "source", source.DatabaseID, "drill_id", drill.ID, "target", drill.TargetAt)
			continue
		}
		result, code := drillStartFailure(err)
		s.log.Warn("dbsched: restore drill did not start", "node", node.ID, "source", source.DatabaseID, "err", err)
		if derr := s.discardDrill(ctx, drill); derr != nil {
			s.log.Warn("dbsched: delete a restore drill's database", "drill_id", drill.ID, "err", derr)
		}
		if ferr := s.store.FinishDrill(ctx, drill.ID, result, time.Time{}, code); ferr != nil {
			s.log.Warn("dbsched: record a restore drill", "drill_id", drill.ID, "err", ferr)
		}
		s.countDrill(result, sample)
		s.cfg.Metrics.drilled(result)
	}
	return nil
}

// pickDrillSource picks the day's source on node: a database that is not a
// drill's, created and backed up before now (a base backup must precede
// the point it recovers to), chosen by a hash of the day, so every database
// is drilled in turn over time and replicas agree.
func pickDrillSource(vms []VM, drills map[string]bool, node, day string, now time.Time) (VM, bool) {
	var best VM
	var bestKey [32]byte
	found := false
	for _, vm := range vms {
		if vm.NodeID != node || !vm.Bootstrapped || vm.LastBackupAt.IsZero() || !vm.LastBackupAt.Before(now) ||
			!vm.CreatedAt.Before(now) || drills[vm.DatabaseID] {
			continue
		}
		key := sha256.Sum256([]byte(day + "/" + vm.DatabaseID))
		if !found || string(key[:]) < string(bestKey[:]) {
			best, bestKey, found = vm, key, true
		}
	}
	return best, found
}

func (s *Scheduler) countDrill(result DrillResult, sample *DrillSample) {
	switch result {
	case DrillOK:
		sample.OK++
	case DrillFailed:
		sample.Failed++
	case DrillSkipped:
		sample.Skipped++
	}
}
