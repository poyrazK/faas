package objectstorage

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReadAdmitter admits gateway-safety reads from per-bucket decisions it
// caches, and batches their counts instead of writing each one
// (ADR-20261030075301). A gateway process keeps one and runs its flush loop.
type ReadAdmitter struct {
	store  state.ObjectReadAdmissionStore
	policy api.ObjectStoragePolicy
	now    func() time.Time
	group  singleflight.Group
	// Cold decision snapshots may run together, but never overlap a flush.
	// A bounded acquisition lets refreshes fall back to stale decisions while
	// a database write stalls; cached admissions do not take this gate.
	storeGate *semaphore.Weighted

	mu               sync.Mutex
	entries          map[string]*readDecision
	pending          map[readUsageKey]*pendingReadUsage
	owners           map[readOwnerKey]*readOwnerUsage
	lastOwnerCleanup time.Time

	decisions *prometheus.CounterVec
	flushes   *prometheus.CounterVec
}

type readDecision struct {
	state.ObjectReadAdmission
	fetchedAt time.Time
	owner     *readOwnerUsage
}

type readUsageKey struct {
	bucket string
	period time.Time
	owner  string
}

// NewReadAdmitter is s3-gatewayd's admitter; its metrics are s3_gateway_*.
func NewReadAdmitter(store state.ObjectReadAdmissionStore, policy api.ObjectStoragePolicy, now func() time.Time) *ReadAdmitter {
	return NewNamedReadAdmitter(store, policy, now, "s3_gateway")
}

// NewNamedReadAdmitter names its metrics <prefix>_read_admission_decisions_total
// and <prefix>_read_usage_flushes_total, so each daemon alerts on its own.
func NewNamedReadAdmitter(store state.ObjectReadAdmissionStore, policy api.ObjectStoragePolicy, now func() time.Time, prefix string) *ReadAdmitter {
	if now == nil {
		now = time.Now
	}
	return &ReadAdmitter{
		store: store, policy: policy, now: now,
		storeGate: semaphore.NewWeighted(math.MaxInt64),
		entries:   map[string]*readDecision{}, pending: map[readUsageKey]*pendingReadUsage{}, owners: map[readOwnerKey]*readOwnerUsage{},
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_read_admission_decisions_total", Help: "Read admission decisions by source: cached, refreshed, stale (served while the database is unreachable) or failed."}, []string{"source"}),
		flushes:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_read_usage_flushes_total", Help: "Batched read count writes by outcome."}, []string{"outcome"}),
	}
}

// Collectors are the admitter's metrics, for the gateway's registry.
func (a *ReadAdmitter) Collectors() []prometheus.Collector {
	return []prometheus.Collector{a.decisions, a.flushes}
}

// AdmittedRead is a read AdmitRead let through, for its egress reservation.
type AdmittedRead struct {
	bucket    string
	period    time.Time
	owner     *readOwnerUsage
	maxEgress int64
	// downloadErr refuses a body (ADR-20261101214152).
	downloadErr error
}

// AdmitRead admits one GET or HEAD of b and counts its request and
// authorization. A refusal is the error the locking admission returns.
func (a *ReadAdmitter) AdmitRead(ctx context.Context, b state.ObjectBucket) (AdmittedRead, error) {
	d, err := a.decision(ctx, b)
	if err != nil {
		return AdmittedRead{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := d.owner
	if a.owners[u.key] != u {
		return AdmittedRead{}, state.ErrObjectUsageStale
	}
	u.lastUsed = a.now().UTC()
	for _, limit := range []struct {
		kind          string
		observed, max int64
	}{
		{"requests", u.recorded.Requests + u.unwritten.Requests, d.MaxRequests},
		{"egress_bytes", u.recorded.EgressBytes + u.unwritten.EgressBytes, d.MaxEgressBytes},
		{"authorizations", u.recorded.Authorizations + u.unwritten.Authorizations, d.MaxAuthorizations},
	} {
		if limit.observed >= limit.max {
			return AdmittedRead{}, &state.ObjectStorageLimitError{Kind: limit.kind, Observed: limit.observed, Limit: limit.max, Cause: state.ErrObjectBudget}
		}
	}
	p := a.pendingLocked(b.ID, d.Period, u)
	p.Requests++
	p.Authorizations++
	u.unwritten.Requests++
	u.unwritten.Authorizations++
	return AdmittedRead{bucket: b.ID, period: d.Period, owner: u, maxEgress: d.MaxEgressBytes, downloadErr: d.DownloadErr}, nil
}

// RefundRead takes back the request and authorization AdmitRead counted for
// r, when the provider answered that the object does not exist. Anonymous
// reads of missing keys miss the CDN cache, so counting them let a stranger
// spend the owner's budget with random paths (object storage audit OS-5). It
// refunds only counts still pending in this process, and reports whether it
// did; a count already flushed stays.
func (a *ReadAdmitter) RefundRead(r AdmittedRead) bool {
	if r.owner == nil || r.bucket == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[readUsageKey{bucket: r.bucket, period: r.period, owner: r.owner.key.owner}]
	if p == nil || p.owner != r.owner || p.Requests < 1 || p.Authorizations < 1 {
		return false
	}
	p.Requests--
	p.Authorizations--
	r.owner.unwritten.Requests--
	r.owner.unwritten.Authorizations--
	return true
}

// ReserveEgress counts an admitted GET's body of bytes against its bucket's
// egress ceiling before it is sent. An unknown length fails closed.
func (a *ReadAdmitter) ReserveEgress(r AdmittedRead, bytes int64) error {
	if bytes < 0 || r.bucket == "" || r.owner == nil {
		return state.ErrObjectUsageStale
	}
	if bytes > 0 && r.downloadErr != nil {
		return r.downloadErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := r.owner
	if a.owners[u.key] != u {
		// A transfer older than the maximum transfer lifetime cannot revive
		// an expired owner's counters.
		return state.ErrObjectUsageStale
	}
	if observed := u.recorded.EgressBytes + u.unwritten.EgressBytes + bytes; observed > r.maxEgress {
		return &state.ObjectStorageLimitError{Kind: "egress_bytes", Observed: observed, Limit: r.maxEgress, Cause: state.ErrObjectBudget}
	}
	u.lastUsed = a.now().UTC()
	p := a.pendingLocked(r.bucket, r.period, u)
	p.EgressBytes += bytes
	u.unwritten.EgressBytes += bytes
	return nil
}

// decision returns b's admitting decision: cached while fresh, otherwise read
// once for every concurrent caller. A failed read keeps serving an admitting
// decision until ObjectReadAdmissionMaxStale, then fails closed.
func (a *ReadAdmitter) decision(ctx context.Context, b state.ObjectBucket) (readDecision, error) {
	now := a.now().UTC()
	period := state.ObjectStoragePeriod(now)
	// Copy under the lock: another refresh may replace the cached decision.
	var entry *readDecision
	a.mu.Lock()
	if cached := a.entries[b.ID]; cached != nil {
		copied := *cached
		entry = &copied
	}
	a.mu.Unlock()
	if entry != nil && entry.Period.Equal(period) && now.Sub(entry.fetchedAt) < api.ObjectReadAdmissionTTL {
		a.decisions.WithLabelValues("cached").Inc()
		return entry.admitted()
	}
	v, err, _ := a.group.Do(b.ID, func() (any, error) {
		// A caller that missed just before another refresh finished uses it.
		a.mu.Lock()
		if cached := a.entries[b.ID]; cached != nil && cached.Period.Equal(period) && now.Sub(cached.fetchedAt) < api.ObjectReadAdmissionTTL {
			fresh := *cached
			a.mu.Unlock()
			return fresh, nil
		}
		a.mu.Unlock()
		// A detached read: one caller's cancellation must not fail the
		// others. The decision is the bucket's, shared by every credential,
		// so it is read without the caller's organization access fence;
		// the gateway checks each credential's fence itself (step 3).
		readCtx, cancel := context.WithTimeout(state.WithoutOrgAccessFence(context.WithoutCancel(ctx)), api.ObjectReadAdmissionTimeout)
		defer cancel()
		if err := a.storeGate.Acquire(readCtx, 1); err != nil {
			return nil, err
		}
		defer a.storeGate.Release(1)
		d, err := a.store.ObjectReadAdmission(readCtx, b.AccountID, b.ID, now, a.policy)
		if err != nil {
			return nil, err
		}
		if d.Err == nil && d.SubjectID == "" {
			return nil, state.ErrObjectUsageStale
		}
		fresh := readDecision{ObjectReadAdmission: d, fetchedAt: now}
		cached := fresh
		a.mu.Lock()
		if d.Err == nil {
			fresh.owner = a.observeOwnerLocked(d, now)
			cached.owner = fresh.owner
		}
		a.storeLocked(b.ID, &cached, now)
		a.mu.Unlock()
		return fresh, nil
	})
	if err != nil {
		if entry != nil && entry.Err == nil && entry.Period.Equal(period) && now.Sub(entry.fetchedAt) < api.ObjectReadAdmissionMaxStale {
			a.decisions.WithLabelValues("stale").Inc()
			return *entry, nil
		}
		a.decisions.WithLabelValues("failed").Inc()
		return readDecision{}, state.ErrObjectUsageStale
	}
	a.decisions.WithLabelValues("refreshed").Inc()
	fresh := v.(readDecision)
	return fresh.admitted()
}

func (d *readDecision) admitted() (readDecision, error) {
	if d.Err != nil {
		return readDecision{}, d.Err
	}
	return *d, nil
}

// storeLocked caches a decision, first dropping expired ones when the cache
// is full; a decision that still does not fit is used once, not cached.
func (a *ReadAdmitter) storeLocked(bucket string, d *readDecision, now time.Time) {
	if _, ok := a.entries[bucket]; !ok && len(a.entries) >= api.MaxObjectReadAdmissionEntries {
		for id, old := range a.entries {
			if now.Sub(old.fetchedAt) >= api.ObjectReadAdmissionMaxStale {
				delete(a.entries, id)
			}
		}
		if len(a.entries) >= api.MaxObjectReadAdmissionEntries {
			return
		}
	}
	a.entries[bucket] = d
}

func (a *ReadAdmitter) pendingLocked(bucket string, period time.Time, owner *readOwnerUsage) *pendingReadUsage {
	key := readUsageKey{bucket: bucket, period: period, owner: owner.key.owner}
	p := a.pending[key]
	if p == nil {
		p = &pendingReadUsage{ObjectReadUsage: state.ObjectReadUsage{BucketID: bucket, Period: period}, owner: owner}
		a.pending[key] = p
	}
	return p
}

// Flush writes the counts admitted since the last flush. On failure they stay
// pending for the next one. Owners' unwritten totals include the in-flight
// batch until commit, so admissions continue against the full usage.
func (a *ReadAdmitter) Flush(ctx context.Context) error {
	if err := a.storeGate.Acquire(ctx, math.MaxInt64); err != nil {
		return err
	}
	defer a.storeGate.Release(math.MaxInt64)
	a.mu.Lock()
	a.pruneOwnersLocked(a.now().UTC())
	pending := a.pending
	batch := make([]state.ObjectReadUsage, 0, len(a.pending))
	for _, p := range a.pending {
		if p.Requests != 0 || p.Authorizations != 0 || p.EgressBytes != 0 {
			batch = append(batch, p.ObjectReadUsage)
		}
	}
	a.pending = map[readUsageKey]*pendingReadUsage{}
	a.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := a.store.RecordObjectReadUsage(ctx, batch)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, u := range pending {
		if err != nil {
			p := a.pendingLocked(u.BucketID, u.Period, u.owner)
			p.Requests += u.Requests
			p.Authorizations += u.Authorizations
			p.EgressBytes += u.EgressBytes
			continue
		}
		u.owner.commit(u.ObjectReadUsage)
	}
	if err != nil {
		a.flushes.WithLabelValues("failed").Inc()
		return err
	}
	a.flushes.WithLabelValues("written").Inc()
	return nil
}

// Run flushes every ObjectReadUsageFlushInterval until ctx ends, then once
// more so a clean shutdown loses nothing.
func (a *ReadAdmitter) Run(ctx context.Context, logf func(error)) {
	ticker := time.NewTicker(api.ObjectReadUsageFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectReadAdmissionTimeout)
			if err := a.Flush(final); err != nil && logf != nil {
				logf(err)
			}
			cancel()
			return
		case <-ticker.C:
			if err := a.Flush(ctx); err != nil && logf != nil && ctx.Err() == nil {
				logf(err)
			}
		}
	}
}
