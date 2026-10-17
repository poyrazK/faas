package dbsched

import (
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Restore drills (ADR-1027). Backups nobody restores are a hope, not a
// backup. Once a day per database node, the scheduler restores one of
// the node's databases to a minute ago into a throwaway database on the
// same node, through the same restore a customer's point-in-time restore
// runs: the source's archive flushed, its base backup extracted, its WAL
// replayed, the copy promoted and backed up in turn. The drill records
// the outcome and how far recovery got, then deletes the throwaway
// database and its archive. Customers never see a drill: its database is
// in no account's catalog. A drill database is never a source: its record
// stays running until the database is gone.

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
	// drillLag is how far behind now a drill recovers to.
	drillLag = time.Minute
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
// parked it (ok) or failed it, or once it has run past DrillTimeout.
func (s *Scheduler) drillOutcome(ctx context.Context, d Drill) (DrillResult, string, time.Time, bool) {
	vm, err := s.store.Get(ctx, d.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		return DrillFailed, "drill_vanished", time.Time{}, true
	case err != nil:
		return "", "", time.Time{}, false
	case vm.State == StateParked && vm.Bootstrapped:
		return DrillOK, "", vm.RestoreRecoveredTo, true
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
		target := now.Add(-drillLag).UTC().Truncate(time.Microsecond)
		source, ok := pickDrillSource(vms, drills, node.ID, day, target)
		if !ok {
			continue
		}
		drill := Drill{ID: uuid.NewSHA1(drillNamespace, []byte(source.DatabaseID+"/"+day)).String(), NodeID: node.ID,
			SourceID: source.DatabaseID, TargetAt: target}
		started, err := s.store.StartDrill(ctx, drill)
		if err != nil {
			s.log.Warn("dbsched: record a restore drill", "node", node.ID, "err", err)
			continue
		}
		if !started {
			// Another replica started it.
			continue
		}
		_, err = s.Restore(ctx, RestoreRequest{
			ProvisionRequest: ProvisionRequest{DatabaseID: drill.ID, Major: source.MajorAt(drill.TargetAt), VcpuCount: 1, MemMiB: min(source.MemMiB, drillMemMiB),
				VolumeBytes: source.VolumeBytes, RestoreWindowSeconds: drillRestoreWindowSeconds},
			Source: source.DatabaseID, Target: drill.TargetAt,
		})
		if err == nil {
			sample.Started++
			s.log.Info("dbsched: restore drill started", "node", node.ID, "source", source.DatabaseID, "drill_id", drill.ID, "target", drill.TargetAt)
			continue
		}
		result, code := DrillFailed, "drill_start_failed"
		if errors.Is(err, ErrNoCapacity) {
			result, code = DrillSkipped, "drill_no_capacity"
		}
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
// drill's, created and backed up before target (a base backup must precede
// the point it recovers to), chosen by a hash of the day, so every database
// is drilled in turn over time and replicas agree.
func pickDrillSource(vms []VM, drills map[string]bool, node, day string, target time.Time) (VM, bool) {
	var best VM
	var bestKey [32]byte
	found := false
	for _, vm := range vms {
		if vm.NodeID != node || !vm.Bootstrapped || vm.LastBackupAt.IsZero() || !vm.LastBackupAt.Before(target) ||
			!vm.CreatedAt.Before(target) || drills[vm.DatabaseID] {
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
