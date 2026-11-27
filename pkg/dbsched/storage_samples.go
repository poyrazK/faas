package dbsched

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbstorage"
)

func appendVolumeSample(samples []dbstorage.Sample, vm VM, role, node string, st *dbguest.StorageStatus, at time.Time) []dbstorage.Sample {
	if st == nil || st.TotalBytes <= 0 || st.FreeBytes < 0 || st.FreeBytes > st.TotalBytes {
		return samples
	}
	return append(samples, dbstorage.Sample{
		DatabaseID: vm.DatabaseID, Role: role, NodeID: node, StateChangedAt: vm.StateChangedAt,
		AllocatedBytes: vm.VolumeBytes, TotalBytes: st.TotalBytes, FreeBytes: st.FreeBytes,
		Low: st.Low, ReadOnly: st.ReadOnly, SampledAt: at,
	})
}

func (s *Scheduler) recordStorageSamples(ctx context.Context, store *PgStore, samples []dbstorage.Sample) {
	if len(samples) == 0 {
		return
	}
	write, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	defer cancel()
	for batch := range slices.Chunk(samples, 256) {
		if err := dbstorage.Record(write, store.pool, batch); err != nil {
			if ctx.Err() == nil {
				s.log.Warn("dbsched: could not persist volume observations", "err", err)
			}
			return
		}
	}
}
