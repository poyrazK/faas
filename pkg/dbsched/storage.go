package dbsched

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbstorage"
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
	// base or launch; Restarted is how many of them this sample restarted,
	// and StandbyRestarted how many stale standbys of current primaries it
	// restarted alone.
	Stale, Restarted, StandbyRestarted int
}

// SampleStorage asks every running database's guest, and its standby's,
// for its volume and its node whether it is stale, and records the
// counts. A guest that does not answer is left out. Each read-only database, and each database whose standby is
// low while it is not, is logged with its id; the metric carries counts
// only. Stale always-on databases restart in their maintenance window
// or after StaleIdle under the default policy, a few per sample;
// scale-to-zero databases refresh on their next wake. A stale standby of a primary that is current restarts alone,
// busy or not, a few per sample (RestartStandby): the primary runs on.
func (s *Scheduler) SampleStorage(ctx context.Context) (StorageSample, error) {
	store, persist := s.store.(*PgStore)
	if persist {
		active, err := store.activeBaseRefreshes(ctx)
		if err != nil {
			s.log.Warn("dbsched: could not read active base refreshes", "err", err)
		} else {
			s.settleBaseRefreshes(ctx, store, active)
		}
	}
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return StorageSample{}, err
	}
	var queryConfiguration, rollingPolicies map[string]bool
	if persist {
		rollingPolicies, err = store.rollingPolicies(ctx)
		if err != nil {
			return StorageSample{}, err
		}
		queryConfiguration, err = store.maintenanceQuerySettings(ctx)
		if err != nil {
			s.log.Warn("dbsched: could not read query insights maintenance policies", "err", err)
		}
	}
	var sample StorageSample
	var stale, staleStandbys, rolling []VM
	var volumes []dbstorage.Sample
	var observations []baseRefreshObservation
	for _, vm := range running {
		primaryAt := s.now()
		resp, ok := s.sampleStatus(ctx, vm.NodeID, vm.DatabaseID)
		if !ok {
			continue
		}
		standbyAt := s.now()
		standby, standbyOK := s.sampleStandby(ctx, vm)
		if persist {
			if vm.AlwaysOn && refreshReady(resp) && (!vm.Replicated || rollingPolicies[vm.DatabaseID]) {
				stale := resp.Stale || standbyOK && standby.Stale
				// An unanswered standby cannot clear its earlier stale observation.
				if !vm.Replicated || stale || standbyOK && standby.State == dbguest.StateStandby {
					observations = append(observations, baseRefreshObservation{vm: vm, stale: stale})
				}
			}
			volumes = appendVolumeSample(volumes, vm, "primary", vm.NodeID, resp.Storage, primaryAt)
			if standbyOK {
				volumes = appendVolumeSample(volumes, vm, "standby", vm.StandbyNode, standby.Storage, standbyAt)
			}
		}
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
			} else if resp.Storage.Low {
				s.log.Warn("dbsched: primary is low on storage", "database_id", vm.DatabaseID,
					"free_bytes", resp.Storage.FreeBytes, "total_bytes", resp.Storage.TotalBytes)
			}
		}
		desired, scheduled := queryConfiguration[vm.DatabaseID]
		queryRestart := scheduled && vm.AlwaysOn && !vm.Replicated && vm.StandbyNode == "" &&
			resp.QueryInsightsSupported && resp.QueryInsightsConfigured != desired
		switch {
		case rollingPolicies[vm.DatabaseID]:
			if resp.Stale || standbyOK && standby.Stale {
				sample.Stale++
				rolling = append(rolling, vm)
			}
		case resp.Stale || queryRestart:
			if resp.Stale {
				sample.Stale++
			}
			stale = append(stale, vm)
		case standbyOK && standby.Stale:
			sample.Stale++
			staleStandbys = append(staleStandbys, vm)
		}
	}
	if persist {
		s.recordStorageSamples(ctx, store, volumes)
		if err := store.recordBaseRefreshObservations(ctx, observations); err != nil {
			s.log.Warn("dbsched: could not record base refresh observations", "err", err)
		}
	}
	if persist {
		s.admitRollingRefreshes(ctx, store, rolling)
	}
	sample.Restarted = s.restartStale(ctx, stale)
	sample.StandbyRestarted = s.restartStaleStandbys(ctx, staleStandbys)
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

// restartStaleStandbys restarts the stale standbys of current primaries,
// at most StaleRestarts of them; it returns how many restarted.
func (s *Scheduler) restartStaleStandbys(ctx context.Context, stale []VM) int {
	restarted := 0
	for _, vm := range stale {
		if restarted >= s.cfg.StaleRestarts {
			break
		}
		s.log.Info("dbsched: restarting a stale standby onto the current base", "database_id", vm.DatabaseID, "node_id", vm.StandbyNode)
		err := s.RestartStandby(ctx, vm.DatabaseID)
		s.cfg.Metrics.restartedStandby(err)
		if err != nil {
			s.log.Warn("dbsched: stale standby restart failed", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		restarted++
	}
	return restarted
}

// restartStale parks and wakes stale always-on databases according to
// their refresh policy, at most StaleRestarts of them. A park stops a
// replicated database's standby after its primary, so both boot the
// current base. It returns how many restarted.
func (s *Scheduler) restartStale(ctx context.Context, stale []VM) int {
	cutoff := s.now().Add(-s.cfg.StaleIdle)
	attempts, restarted := 0, 0
	for _, vm := range stale {
		if attempts >= s.cfg.StaleRestarts {
			break
		}
		if store, ok := s.store.(*PgStore); ok && vm.AlwaysOn && !vm.Replicated && vm.StandbyNode == "" {
			attempted, completed, err := s.restartStaleSingle(ctx, store, vm, cutoff)
			if attempted {
				attempts++
				s.cfg.Metrics.restartedStale(err)
			}
			if err != nil {
				s.log.Warn("dbsched: stale restart failed", "database_id", vm.DatabaseID, "err", err)
			}
			if completed {
				restarted++
			}
			continue
		}
		if !vm.AlwaysOn || vm.LastActiveAt.After(cutoff) || s.importing(ctx, vm) {
			continue
		}
		s.log.Info("dbsched: restarting a stale database onto the current base", "database_id", vm.DatabaseID)
		attempts++
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
