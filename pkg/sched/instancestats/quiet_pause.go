package instancestats

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// quietPausedSnapshot is the set of instances each node reported
// quiet-paused (ADR-1151) in one tick, and when that tick read them.
type quietPausedSnapshot struct {
	byNode map[string]map[string]struct{}
	asOf   time.Time
}

// publishQuietPaused records which instances each node reported
// quiet-paused this tick, for schedd's quiet pause admission.
func (p *Poller) publishQuietPaused(rows []InstanceStat, asOf time.Time) {
	if p.Reader == nil {
		return
	}
	byNode := make(map[string]map[string]struct{})
	for _, row := range rows {
		if !row.QuietPaused || row.NodeID == "" {
			continue
		}
		set := byNode[row.NodeID]
		if set == nil {
			set = make(map[string]struct{})
			byNode[row.NodeID] = set
		}
		set[row.InstanceID] = struct{}{}
	}
	p.Reader.quiet.Store(&quietPausedSnapshot{byNode: byNode, asOf: asOf})
}

// QuietPaused returns the instances the latest tick saw quiet-paused on a
// node and when that tick read them. ok is false when no tick has run or
// the latest is older than QuietPauseSampleMaxAgeMillis; admission then
// counts every booking. A node with no paused instance returns an empty
// set with ok true.
func (r *Reader) QuietPaused(nodeID string, now time.Time) (map[string]struct{}, time.Time, bool) {
	snap := r.quiet.Load()
	if snap == nil || now.Sub(snap.asOf) > time.Duration(api.QuietPauseSampleMaxAgeMillis)*time.Millisecond {
		return nil, time.Time{}, false
	}
	return snap.byNode[nodeID], snap.asOf, true
}
