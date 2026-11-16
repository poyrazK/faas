package logarchive

import "context"

// maxRetiredPending bounds the instances waiting for an early upload. Past
// it a retirement waits for the regular FlushInterval tick, as before.
const maxRetiredPending = 4096

// ShipInstance asks Run to upload instance's spooled lines now instead of
// on the next FlushInterval tick. A parked or destroyed instance has no live
// ring, so `gregale logs` reads its lines from the bucket; with only the
// 5-minute tick, logs of an instance parked moments ago read as an archive
// gap (prod 2026-11-16: 147 logarchive.readback.missing in 48 h, read
// seconds after park). Only that instance's files are uploaded. Safe to call
// from any goroutine; it never blocks.
func (s *Shipper) ShipInstance(instance string) {
	if s == nil || s.kick == nil || instance == "" {
		return
	}
	s.retiredMu.Lock()
	if s.retired == nil {
		s.retired = make(map[string]struct{})
	}
	if len(s.retired) < maxRetiredPending {
		s.retired[instance] = struct{}{}
	}
	s.retiredMu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// shipRetired uploads the spool files of the instances ShipInstance named.
func (s *Shipper) shipRetired(ctx context.Context) {
	s.retiredMu.Lock()
	pending := s.retired
	s.retired = nil
	s.retiredMu.Unlock()
	if len(pending) == 0 {
		return
	}
	if _, _, err := s.runFiles(ctx, func(f FileInfo) bool {
		_, ok := pending[f.Instance]
		return ok
	}); err != nil && ctx.Err() == nil {
		s.log.Warn("logarchive.retired_flush_failed", "instances", len(pending), "err", err)
	}
	s.metrics.SetLocalBytes(s.spool.LocalBytes())
}
