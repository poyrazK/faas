package objectstorage

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type readOwnerKey struct {
	owner  string
	period time.Time
}

type readUsageCounts struct {
	Requests, Authorizations, EgressBytes int64
}

// All buckets billed to one owner share these counts. Recorded
// counts include successful local flushes and the latest database snapshot;
// unwritten counts include both pending and currently flushing batches.
type readOwnerUsage struct {
	key                 readOwnerKey
	recorded, unwritten readUsageCounts
	lastUsed            time.Time
}

type pendingReadUsage struct {
	state.ObjectReadUsage
	owner *readOwnerUsage
}

func (a *ReadAdmitter) observeOwnerLocked(d state.ObjectReadAdmission, now time.Time) *readOwnerUsage {
	key := readOwnerKey{owner: d.SubjectID, period: d.Period}
	u := a.owners[key]
	if u == nil {
		u = &readOwnerUsage{key: key}
		a.owners[key] = u
	}
	// The store gate prevents the snapshot overlapping a local flush. Taking
	// maxima also keeps an older bucket decision from lowering shared usage.
	u.recorded.Requests = max(u.recorded.Requests, d.Requests)
	u.recorded.Authorizations = max(u.recorded.Authorizations, d.Authorizations)
	u.recorded.EgressBytes = max(u.recorded.EgressBytes, d.EgressBytes)
	u.lastUsed = now
	return u
}

// commit moves one durable batch out of the owner's unwritten counts, without
// changing the total seen by concurrent admissions. The caller holds a.mu.
func (u *readOwnerUsage) commit(batch state.ObjectReadUsage) {
	u.recorded.Requests += batch.Requests
	u.recorded.Authorizations += batch.Authorizations
	u.recorded.EgressBytes += batch.EgressBytes
	u.unwritten.Requests -= batch.Requests
	u.unwritten.Authorizations -= batch.Authorizations
	u.unwritten.EgressBytes -= batch.EgressBytes
}

func (a *ReadAdmitter) pruneOwnersLocked(now time.Time) {
	if now.Sub(a.lastOwnerCleanup) < api.ObjectReadAdmissionMaxStale {
		return
	}
	a.lastOwnerCleanup = now
	for key, u := range a.owners {
		// A read may reserve its body after a long provider request, including
		// across month end. Keep its owner through the maximum transfer
		// lifetime, and never discard usage a failed flush still owes.
		if now.Sub(u.lastUsed) > api.MaxObjectTransferTimeout && u.unwritten == (readUsageCounts{}) {
			delete(a.owners, key)
		}
	}
}
