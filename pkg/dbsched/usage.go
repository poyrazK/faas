package dbsched

import (
	"context"
	"fmt"
	"time"
)

// Usage metering (ADR-1027). The store keeps a ledger per database that
// outlives it: rate intervals for compute (vCPUs while the VM runs, from
// waking until parked), storage (provisioned volume bytes while the
// database exists) and history (archive bytes, sampled from dbbackupd),
// and hourly egress byte counts from pgproxyd. A window's usage is the
// overlap of those intervals with it, so asking again for the same past
// window gives the same answer.

// Ledger meters. A production database's standby holds real resources
// too: its vCPUs while its VM runs and a second copy of the volume while it
// has a node. Its meters count into compute units and storage (not active
// seconds), so the account's usage ceilings see what the database holds.
const (
	MeterCompute        = "compute"
	MeterStorage        = "storage"
	MeterHistory        = "history"
	MeterEgress         = "egress"
	MeterStandbyCompute = "standby_compute"
	MeterStandbyStorage = "standby_storage"
)

// rateMeters are the meters a deleted database's ledger closes.
var rateMeters = []string{MeterCompute, MeterStorage, MeterHistory, MeterStandbyCompute, MeterStandbyStorage}

// standbyRates are the standby's open rates vm implies: compute while its
// VM runs, storage while it has a node.
func standbyRates(vm VM) map[string]int64 {
	rates := map[string]int64{}
	if vm.StandbyRunning {
		rates[MeterStandbyCompute] = computeRate(vm)
	}
	if vm.StandbyNode != "" {
		rates[MeterStandbyStorage] = vm.VolumeBytes
	}
	return rates
}

// UsageRow is one ledger row. Rate meters have a per-second rate over
// [Start, End) (End zero: still open); egress rows count Rate bytes in the
// hour starting at Start.
type UsageRow struct {
	Meter string
	Start time.Time
	End   time.Time
	Rate  int64
}

// Usage is a database's consumption in one window.
type Usage struct {
	// ActiveSeconds is how long its VM ran.
	ActiveSeconds      int64
	ComputeUnitSeconds int64
	StorageByteSeconds int64
	HistoryByteSeconds int64
	EgressBytes        int64
}

// sumUsage totals the rows' overlap with [from, to). Open intervals run
// until now, never into a window's future. Egress counts the hours that
// start inside the window; collectors ask for whole hours.
func sumUsage(rows []UsageRow, from, to, now time.Time) Usage {
	var u Usage
	for _, r := range rows {
		if r.Meter == MeterEgress {
			if !r.Start.Before(from) && r.Start.Before(to) {
				u.EgressBytes += r.Rate
			}
			continue
		}
		start, end := r.Start, r.End
		if end.IsZero() {
			end = now
		}
		if end.After(to) {
			end = to
		}
		if start.Before(from) {
			start = from
		}
		if !end.After(start) {
			continue
		}
		ms := end.Sub(start).Milliseconds()
		// rate*ms can overflow for terabyte volumes over a day; split it.
		quantity := r.Rate*(ms/1000) + r.Rate*(ms%1000)/1000
		switch r.Meter {
		case MeterCompute:
			u.ActiveSeconds += ms / 1000
			u.ComputeUnitSeconds += quantity
		case MeterStandbyCompute:
			u.ComputeUnitSeconds += quantity
		case MeterStorage, MeterStandbyStorage:
			u.StorageByteSeconds += quantity
		case MeterHistory:
			u.HistoryByteSeconds += quantity
		}
	}
	return u
}

// computeRate is what a running VM charges per second: its vCPUs, the
// compute unit of the class shapes (ADR-1027).
func computeRate(vm VM) int64 { return int64(vm.VcpuCount) }

// usageHour is the egress bucket an instant falls in.
func usageHour(at time.Time) time.Time { return at.UTC().Truncate(time.Hour) }

// usageRetention is how long the ledger keeps closed rows: far past the
// collector's correction tail, which re-reads only recent windows.
const usageRetention = 90 * 24 * time.Hour

// Usage reports a database's consumption over [from, to), deleted or not.
func (s *Scheduler) Usage(ctx context.Context, id string, from, to time.Time) (Usage, error) {
	if from.IsZero() || !to.After(from) {
		return Usage{}, fmt.Errorf("%w: empty usage window", ErrInvalid)
	}
	return s.store.Usage(ctx, id, from, to)
}

// RecordEgress adds the bytes pgproxyd sent a database's clients.
func (s *Scheduler) RecordEgress(ctx context.Context, id string, bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("%w: negative egress", ErrInvalid)
	}
	if bytes == 0 {
		return nil
	}
	return s.store.AddEgress(ctx, id, bytes, s.now())
}

// SampleArchives records each database's archive size on its node as the
// restore-history meter. Databases on unavailable nodes keep their last
// sample until deleted (PruneLostArchives trims their bucket copy). It
// reports how many databases it sampled.
func (s *Scheduler) SampleArchives(ctx context.Context) (int, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return 0, err
	}
	vms, err := s.store.List(ctx, StateProvisioning, StateParked, StateWaking, StateRunning, StateParking, StateFailed)
	if err != nil {
		return 0, err
	}
	byNode := map[string][]string{}
	for _, vm := range vms {
		byNode[vm.NodeID] = append(byNode[vm.NodeID], vm.DatabaseID)
	}
	sampled := 0
	at := s.now()
	for _, n := range nodes {
		if n.Lifecycle == NodeUnavailable || len(byNode[n.ID]) == 0 {
			continue
		}
		sizes, err := s.vmm.ArchiveBytes(ctx, n, byNode[n.ID])
		if err != nil {
			s.log.Warn("dbsched: size archives", "node_id", n.ID, "err", err)
			continue
		}
		for id, bytes := range sizes {
			if err := s.store.SampleUsage(ctx, id, MeterHistory, bytes, at); err != nil {
				s.log.Warn("dbsched: record archive size", "database_id", id, "err", err)
				continue
			}
			sampled++
		}
	}
	return sampled, nil
}
