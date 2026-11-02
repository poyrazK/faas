package objectstorage

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

	mu      sync.Mutex
	entries map[string]*readDecision
	pending map[readUsageKey]*state.ObjectReadUsage

	decisions *prometheus.CounterVec
	flushes   *prometheus.CounterVec
}

type readDecision struct {
	state.ObjectReadAdmission
	fetchedAt time.Time
}

type readUsageKey struct {
	bucket string
	period time.Time
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
		entries: map[string]*readDecision{}, pending: map[readUsageKey]*state.ObjectReadUsage{},
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
	bucket            string
	period            time.Time
	egress, maxEgress int64
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
	p := a.pendingLocked(b.ID, d.Period)
	for _, limit := range []struct {
		kind          string
		observed, max int64
	}{
		{"requests", d.Requests + p.Requests, d.MaxRequests},
		{"egress_bytes", d.EgressBytes + p.EgressBytes, d.MaxEgressBytes},
		{"authorizations", d.Authorizations + p.Authorizations, d.MaxAuthorizations},
	} {
		if limit.observed >= limit.max {
			return AdmittedRead{}, &state.ObjectStorageLimitError{Kind: limit.kind, Observed: limit.observed, Limit: limit.max, Cause: state.ErrObjectBudget}
		}
	}
	p.Requests++
	p.Authorizations++
	return AdmittedRead{bucket: b.ID, period: d.Period, egress: d.EgressBytes, maxEgress: d.MaxEgressBytes, downloadErr: d.DownloadErr}, nil
}

// ReserveEgress counts an admitted GET's body of bytes against its bucket's
// egress ceiling before it is sent. An unknown length fails closed.
func (a *ReadAdmitter) ReserveEgress(r AdmittedRead, bytes int64) error {
	if bytes < 0 || r.bucket == "" {
		return state.ErrObjectUsageStale
	}
	if bytes > 0 && r.downloadErr != nil {
		return r.downloadErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// A flush since admission moved counts from pending into the cached
	// decision; count them from there.
	written := r.egress
	if entry := a.entries[r.bucket]; entry != nil && entry.Period.Equal(r.period) {
		written = max(written, entry.EgressBytes)
	}
	p := a.pendingLocked(r.bucket, r.period)
	if observed := written + p.EgressBytes + bytes; observed > r.maxEgress {
		return &state.ObjectStorageLimitError{Kind: "egress_bytes", Observed: observed, Limit: r.maxEgress, Cause: state.ErrObjectBudget}
	}
	p.EgressBytes += bytes
	return nil
}

// decision returns b's admitting decision: cached while fresh, otherwise read
// once for every concurrent caller. A failed read keeps serving an admitting
// decision until ObjectReadAdmissionMaxStale, then fails closed.
func (a *ReadAdmitter) decision(ctx context.Context, b state.ObjectBucket) (state.ObjectReadAdmission, error) {
	now := a.now().UTC()
	period := state.ObjectStoragePeriod(now)
	// Copy under the lock: Flush updates cached decisions in place.
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
		d, err := a.store.ObjectReadAdmission(readCtx, b.AccountID, b.ID, now, a.policy)
		if err != nil {
			return nil, err
		}
		fresh := readDecision{ObjectReadAdmission: d, fetchedAt: now}
		cached := fresh
		a.mu.Lock()
		a.storeLocked(b.ID, &cached, now)
		a.mu.Unlock()
		return fresh, nil
	})
	if err != nil {
		if entry != nil && entry.Err == nil && entry.Period.Equal(period) && now.Sub(entry.fetchedAt) < api.ObjectReadAdmissionMaxStale {
			a.decisions.WithLabelValues("stale").Inc()
			return entry.ObjectReadAdmission, nil
		}
		a.decisions.WithLabelValues("failed").Inc()
		return state.ObjectReadAdmission{}, state.ErrObjectUsageStale
	}
	a.decisions.WithLabelValues("refreshed").Inc()
	fresh := v.(readDecision)
	return fresh.admitted()
}

func (d *readDecision) admitted() (state.ObjectReadAdmission, error) {
	if d.Err != nil {
		return state.ObjectReadAdmission{}, d.Err
	}
	return d.ObjectReadAdmission, nil
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

func (a *ReadAdmitter) pendingLocked(bucket string, period time.Time) *state.ObjectReadUsage {
	key := readUsageKey{bucket: bucket, period: period}
	p := a.pending[key]
	if p == nil {
		p = &state.ObjectReadUsage{BucketID: bucket, Period: period}
		a.pending[key] = p
	}
	return p
}

// Flush writes the counts admitted since the last flush. On failure they stay
// pending for the next one. Written counts are added to the cached decisions,
// so ceilings stay tight until the next refresh reads them back.
func (a *ReadAdmitter) Flush(ctx context.Context) error {
	a.mu.Lock()
	batch := make([]state.ObjectReadUsage, 0, len(a.pending))
	for _, p := range a.pending {
		if p.Requests != 0 || p.Authorizations != 0 || p.EgressBytes != 0 {
			batch = append(batch, *p)
		}
	}
	a.pending = map[readUsageKey]*state.ObjectReadUsage{}
	a.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := a.store.RecordObjectReadUsage(ctx, batch)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, u := range batch {
		if err != nil {
			p := a.pendingLocked(u.BucketID, u.Period)
			p.Requests += u.Requests
			p.Authorizations += u.Authorizations
			p.EgressBytes += u.EgressBytes
			continue
		}
		if entry := a.entries[u.BucketID]; entry != nil && entry.Period.Equal(u.Period) {
			entry.Requests += u.Requests
			entry.Authorizations += u.Authorizations
			entry.EgressBytes += u.EgressBytes
		}
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
