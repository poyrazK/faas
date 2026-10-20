package dbsched

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// storageProbeTimeout bounds one guest's answer to a sample.
const storageProbeTimeout = 5 * time.Second

// StorageSample counts running databases by how short of space they are
// (ADR-1027 storage guard) and how many run an outdated base or launch.
type StorageSample struct {
	// Sampled is how many running databases answered.
	Sampled int
	// Low is how many are short of the space that would end read-only
	// (read-only ones included).
	Low int
	// ReadOnly is how many primaries the guard turned read-only.
	ReadOnly int
	// StandbyLow is how many standbys are short of that space while their
	// primary is not. The guard governs only the primary, which carries a
	// ballast the standby lacks, so a standby normally has more room; one
	// with less holds data its primary does not (WAL its restartpoints
	// have not let go), and fills first. A full standby stops replaying,
	// so the primary drops to asynchronous and loses its failover.
	StandbyLow int
	// Stale is how many run, or have a standby that runs, an outdated
	// base or launch; Restarted is how many of them this sample restarted.
	Stale, Restarted int
}

// SampleStorage asks every running database's guest, and its standby's,
// for its volume and its node whether it is stale, and records the
// counts. It never wakes a database, and a guest that does not answer is
// left out. Each read-only database, and each database whose standby is
// low while it is not, is logged with its id; the metric carries counts
// only. Stale
// always-on databases idle for StaleIdle restart onto the current base,
// a few per sample; scale-to-zero ones do on their next idle park.
func (s *Scheduler) SampleStorage(ctx context.Context) (StorageSample, error) {
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return StorageSample{}, err
	}
	var sample StorageSample
	var stale []VM
	for _, vm := range running {
		resp, ok := s.sampleStatus(ctx, vm.NodeID, vm.DatabaseID)
		if !ok {
			continue
		}
		standby, standbyOK := s.sampleStandby(ctx, vm)
		if st := standby.Storage; standbyOK && st != nil && st.Low && (resp.Storage == nil || !resp.Storage.Low) {
			sample.StandbyLow++
			s.log.Warn("dbsched: standby is short of storage, its primary is not", "database_id", vm.DatabaseID,
				"node_id", vm.StandbyNode, "free_bytes", st.FreeBytes, "total_bytes", st.TotalBytes)
		}
		if resp.Storage != nil {
			sample.Sampled++
			if resp.Storage.Low || resp.Storage.ReadOnly {
				sample.Low++
			}
			if resp.Storage.ReadOnly {
				sample.ReadOnly++
				s.log.Warn("dbsched: database is read-only, short of storage", "database_id", vm.DatabaseID,
					"free_bytes", resp.Storage.FreeBytes, "total_bytes", resp.Storage.TotalBytes)
			}
		}
		if resp.Stale || standbyOK && standby.Stale {
			sample.Stale++
			stale = append(stale, vm)
		}
	}
	sample.Restarted = s.restartStale(ctx, stale)
	s.cfg.Metrics.sampledStorage(sample)
	return sample, nil
}

// sampleStatus asks one VM's node for its status.
func (s *Scheduler) sampleStatus(ctx context.Context, nodeID, id string) (dbguest.Response, bool) {
	node, err := s.node(ctx, nodeID)
	if err != nil {
		return dbguest.Response{}, false
	}
	probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	defer cancel()
	resp, err := s.vmm.Control(probe, node, id, dbguest.Request{Op: dbguest.OpStatus})
	return resp, err == nil
}

// sampleStandby asks a replicated database's running standby for its
// status: its volume, and whether it runs an outdated base or launch (its
// node may have been released before the primary's).
func (s *Scheduler) sampleStandby(ctx context.Context, vm VM) (dbguest.Response, bool) {
	if !vm.Replicated || vm.StandbyNode == "" || !vm.StandbyRunning {
		return dbguest.Response{}, false
	}
	return s.sampleStatus(ctx, vm.StandbyNode, vm.DatabaseID)
}

// Restart restarts a running database in place, onto its node's current
// base (ADR-1027): it parks it, which stops a standby after its primary,
// and wakes it again. A parked database is left as it is, since its next
// wake boots the current base anyway; any other state is ErrConflict.
func (s *Scheduler) Restart(ctx context.Context, id string) (VM, error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return VM{}, err
	}
	switch vm.State {
	case StateParked:
		return vm, nil
	case StateRunning:
	default:
		return VM{}, fmt.Errorf("%w: the database is %s", ErrConflict, vm.State)
	}
	if err := s.Park(ctx, id); err != nil {
		return VM{}, err
	}
	return s.Wake(ctx, id)
}

// restartStale parks and wakes stale always-on databases that have been
// idle for StaleIdle, at most StaleRestarts of them. A park stops a
// replicated database's standby after its primary, so both boot the
// current base. It returns how many restarted.
func (s *Scheduler) restartStale(ctx context.Context, stale []VM) int {
	cutoff := s.now().Add(-s.cfg.StaleIdle)
	restarted := 0
	for _, vm := range stale {
		if restarted >= s.cfg.StaleRestarts {
			break
		}
		if !vm.AlwaysOn || vm.LastActiveAt.After(cutoff) || s.importing(ctx, vm) {
			continue
		}
		s.log.Info("dbsched: restarting a stale database onto the current base", "database_id", vm.DatabaseID)
		_, err := s.Restart(ctx, vm.DatabaseID)
		s.cfg.Metrics.restartedStale(err)
		if err != nil {
			// The keep-warm sweep wakes an always-on database left parked.
			s.log.Warn("dbsched: stale restart failed", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		restarted++
	}
	return restarted
}
