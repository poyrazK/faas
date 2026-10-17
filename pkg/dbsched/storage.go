package dbsched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// storageProbeTimeout bounds one guest's storage answer.
const storageProbeTimeout = 5 * time.Second

// StorageSample counts running databases by how short of space they are
// (ADR-1027 storage guard).
type StorageSample struct {
	// Sampled is how many running databases answered.
	Sampled int
	// Low is how many are short of the space that would end read-only
	// (read-only ones included).
	Low int
	// ReadOnly is how many primaries the guard turned read-only.
	ReadOnly int
}

// SampleStorage asks every running database's guest for its volume and
// records how many are short of space. It never wakes a database, and a
// guest that does not answer is left out. Each read-only database is
// logged with its id; the metric carries counts only.
func (s *Scheduler) SampleStorage(ctx context.Context) (StorageSample, error) {
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return StorageSample{}, err
	}
	var sample StorageSample
	for _, vm := range running {
		node, err := s.node(ctx, vm.NodeID)
		if err != nil {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
		resp, err := s.vmm.Control(probe, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
		cancel()
		if err != nil || resp.Storage == nil {
			continue
		}
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
	s.cfg.Metrics.sampledStorage(sample)
	return sample, nil
}
