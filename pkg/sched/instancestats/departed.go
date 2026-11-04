package instancestats

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// departedStat is the last snapshot row of an instance that has left the
// snapshot, and when it left.
type departedStat struct {
	row InstanceStat
	at  time.Time
}

// trackDeparted remembers the last row of every instance in prev that is not
// in next, so meterd can still checkpoint its final host-interface byte
// counters (ADR-20261103121407). vmmd forgets an instance's counters at
// teardown and meterd reads them only once a minute, so the bytes sent since
// meterd's last read were otherwise never metered. A row that reappears is
// dropped from the departed set; entries expire after
// api.MeterFinalNetworkReadingRetention. Only rows with a valid network
// counter are kept: nothing else in a departed row is consumed.
func (r *Reader) trackDeparted(prev *[]InstanceStat, next []InstanceStat) {
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	present := make(map[string]struct{}, len(next))
	for _, row := range next {
		present[row.InstanceID] = struct{}{}
	}
	r.departedMu.Lock()
	defer r.departedMu.Unlock()
	if r.departed == nil {
		r.departed = make(map[string]departedStat)
	}
	for id, d := range r.departed {
		if _, back := present[id]; back || now.Sub(d.at) >= api.MeterFinalNetworkReadingRetention {
			delete(r.departed, id)
		}
	}
	if prev == nil {
		return
	}
	for _, row := range *prev {
		if row.InstanceID == "" {
			continue
		}
		if _, still := present[row.InstanceID]; still {
			continue
		}
		if row.TX != Valid && row.RX != Valid {
			continue
		}
		r.departed[row.InstanceID] = departedStat{row: row, at: now}
	}
}

// SnapshotDeparted returns the last row of each instance that left the
// snapshot within api.MeterFinalNetworkReadingRetention, in (AppID,
// InstanceID) order. Only its cumulative network counters are meaningful:
// the instance is gone, so callers must not read it as live (scheddgrpc
// serves these rows to meterd alone, ListInstanceStats).
func (r *Reader) SnapshotDeparted() []InstanceStat {
	if r == nil {
		return nil
	}
	r.departedMu.Lock()
	out := make([]InstanceStat, 0, len(r.departed))
	for _, d := range r.departed {
		out = append(out, d.row)
	}
	r.departedMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}
