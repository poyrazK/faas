// adr: 520
package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func drainTLSLookupBucket(ctx context.Context, p *OnDemandTLSPolicy) {
	for n := range api.OnDemandTLSAskLookupBurst {
		p.Decide(ctx, fmt.Sprintf("scan-%d.unregistered.example.org", n))
	}
}

func TestOnDemandTLSPolicyRestartProtectsDurableHostsDuringFlood(t *testing.T) {
	s, first, _ := onDemandTLSFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	first.SetNow(func() time.Time { return now })
	hosts := []string{"shop.example.com", "a.tenants.example.net"}
	for _, host := range hosts {
		if allowed, decision := first.Decide(ctx, host); !allowed {
			t.Fatalf("initial %s admission: %s", host, decision)
		}
	}
	for restart := range 3 {
		p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
		p.SetNow(func() time.Time { return now })
		drainTLSLookupBucket(ctx, p)
		if allowed, decision := p.Decide(ctx, "new.unregistered.example.org"); allowed || decision != OnDemandTLSDenyOverload {
			t.Fatalf("restart %d lost unknown-host protection: %v %s", restart, allowed, decision)
		}
		for _, host := range hosts {
			if allowed, decision := p.Decide(ctx, host); !allowed {
				t.Fatalf("restart %d refused durable %s: %s", restart, host, decision)
			}
		}
	}
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	p.SetNow(func() time.Time { return now })
	drainTLSLookupBucket(ctx, p)
	if _, err := s.MarkCustomDomainDNSDrifted(ctx, hosts[0], now, "wrong CNAME"); err != nil {
		t.Fatal(err)
	}
	if allowed, decision := p.Decide(ctx, hosts[0]); allowed || decision != OnDemandTLSDenyUnverified {
		t.Fatalf("durable hint bypassed DNS revocation: %v %s", allowed, decision)
	}
	if err := s.SetCustomDomainTLSAllowedHosts(ctx, "*.tenants.example.net", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if allowed, decision := p.Decide(ctx, hosts[1]); allowed || decision != OnDemandTLSDenyNotAllowlisted {
		t.Fatalf("durable hint bypassed host allowlist: %v %s", allowed, decision)
	}
}

type tlsHintReadStore struct {
	OnDemandTLSStore
	fail error
}

type tlsHintResumableStore struct {
	OnDemandTLSStore
	slow   bool
	starts atomic.Int32
	failed atomic.Bool
}

func (s *tlsHintResumableStore) ListCustomDomainTLSKnownHosts(ctx context.Context, after string, limit int) ([]string, error) {
	if after == "" {
		s.starts.Add(1)
	}
	if s.slow {
		timer := time.NewTimer(1100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	} else if after != "" && !s.failed.Swap(true) {
		return nil, errors.New("temporary second-page failure")
	}
	if after != "" {
		return []string{"shop.example.com"}, nil
	}
	rows := make([]string, limit)
	for n := range rows {
		rows[n] = fmt.Sprintf("known-%06d.example.com", n)
	}
	rows[0] = "a.tenants.example.net"
	return rows, nil
}

func TestOnDemandTLSKnownHintsResumeCompletedPages(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", slow), func(t *testing.T) {
			s, initial, _ := onDemandTLSFixture(t)
			for _, host := range []string{"shop.example.com", "a.tenants.example.net"} {
				if allowed, decision := initial.Decide(t.Context(), host); !allowed {
					t.Fatalf("seed durable admission for %s: %s", host, decision)
				}
			}
			store := &tlsHintResumableStore{OnDemandTLSStore: s, slow: slow}
			p := NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
			now := time.Now().UTC()
			p.SetNow(func() time.Time { return now })
			if allowed, decision := p.Decide(t.Context(), "shop.example.com"); allowed || decision != OnDemandTLSDenyError {
				t.Fatalf("incomplete initial inventory: %v %s", allowed, decision)
			}
			now = now.Add(api.OnDemandTLSAskKnownHostRetry)
			if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
				t.Fatalf("bounded retry did not finish inventory: %s", decision)
			}
			if got := store.starts.Load(); got != 1 {
				t.Fatalf("completed first page was read %d times, want 1", got)
			}
			drainTLSLookupBucket(t.Context(), p)
			for _, host := range []string{"shop.example.com", "a.tenants.example.net"} {
				if allowed, decision := p.Decide(t.Context(), host); !allowed {
					t.Fatalf("resumed inventory lost durable admission for %s under flood: %s", host, decision)
				}
			}
		})
	}
}

func (s *tlsHintReadStore) ListCustomDomainTLSKnownHosts(ctx context.Context, after string, limit int) ([]string, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return s.OnDemandTLSStore.(state.CustomDomainTLSKnownHostReader).ListCustomDomainTLSKnownHosts(ctx, after, limit)
}

func TestOnDemandTLSPolicyKnownHintsRetryAndRetainPreviousSnapshot(t *testing.T) {
	s, _, _ := onDemandTLSFixture(t)
	now := time.Now().UTC()
	store := &tlsHintReadStore{OnDemandTLSStore: s, fail: errors.New("temporary hint read failure")}
	p := NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	p.SetNow(func() time.Time { return now })
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); allowed || decision != OnDemandTLSDenyError {
		t.Fatalf("initial hint read failure: %v %s", allowed, decision)
	}
	store.fail = nil
	now = now.Add(api.OnDemandTLSAskKnownHostRetry)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("hint read did not recover: %s", decision)
	}
	store.fail = errors.New("later hint read failure")
	now = now.Add(api.OnDemandTLSAskKnownHostRefresh)
	drainTLSLookupBucket(t.Context(), p)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("refresh failure discarded prior hints: %s", decision)
	}
}

func TestOnDemandTLSPolicyHintFalsePositiveGrantsNoPermission(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	if err := p.refreshKnownHostHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A filter collision may skip the bucket, but cannot authorize a name.
	p.hints.add("pending.example.com")
	p.hints.add("unknown.example.com")
	drainTLSLookupBucket(t.Context(), p)
	for _, tc := range []struct{ host, decision string }{
		{"pending.example.com", OnDemandTLSDenyUnverified},
		{"unknown.example.com", OnDemandTLSDenyUnknown},
	} {
		if allowed, decision := p.Decide(t.Context(), tc.host); allowed || decision != tc.decision {
			t.Fatalf("hint granted permission for %s: %v %s", tc.host, allowed, decision)
		}
	}
}

type tlsHintPageReader struct {
	hosts []string
	reads int
}

func (s *tlsHintPageReader) ListCustomDomainTLSKnownHosts(_ context.Context, after string, limit int) ([]string, error) {
	s.reads++
	start, found := slices.BinarySearch(s.hosts, after)
	if found {
		start++
	}
	return s.hosts[start:min(start+limit, len(s.hosts))], nil
}

func TestOnDemandTLSKnownHintsLoadEveryPage(t *testing.T) {
	reader := &tlsHintPageReader{}
	for n := range 2*api.OnDemandTLSAskKnownHostPageSize + 5 {
		reader.hosts = append(reader.hosts, fmt.Sprintf("known-%06d.example.com", n))
	}
	hints, err := loadTLSKnownHostHints(t.Context(), reader, &tlsKnownHostScan{})
	if err != nil {
		t.Fatal(err)
	}
	if reader.reads != 3 {
		t.Fatalf("paged inventory reads = %d, want 3", reader.reads)
	}
	for _, host := range reader.hosts {
		if !hints.contains(host) {
			t.Fatalf("durable host beyond a page boundary was omitted: %s", host)
		}
	}
}

func TestOnDemandTLSKnownHintsNormalizeLegacyNames(t *testing.T) {
	reader := &tlsHintPageReader{hosts: []string{"Shop.Example.COM"}}
	hints, err := loadTLSKnownHostHints(t.Context(), reader, &tlsKnownHostScan{})
	if err != nil {
		t.Fatal(err)
	}
	if !hints.contains("shop.example.com") {
		t.Fatal("case-insensitive durable hostname lost its restart protection")
	}
}

type tlsHintBlockingStore struct {
	OnDemandTLSStore
	started chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (s *tlsHintBlockingStore) ListCustomDomainTLSKnownHosts(ctx context.Context, after string, limit int) ([]string, error) {
	if s.reads.Add(1) == 1 {
		close(s.started)
	}
	select {
	case <-s.release:
		return s.OnDemandTLSStore.(state.CustomDomainTLSKnownHostReader).ListCustomDomainTLSKnownHosts(ctx, after, limit)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestOnDemandTLSKnownHintsRefreshDoesNotBlockKnownHost(t *testing.T) {
	s, initial, _ := onDemandTLSFixture(t)
	if allowed, decision := initial.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("seed durable admission: %s", decision)
	}
	store := &tlsHintBlockingStore{OnDemandTLSStore: s, started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(store.release) }) })
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("load initial hints: %s", decision)
	}
	p.store = store
	p.mu.Lock()
	p.hintsNextLoad = time.Time{}
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if allowed, decision := p.Decide(ctx, "shop.example.com"); !allowed {
		t.Fatalf("known host blocked on background refresh: %s", decision)
	}
	select {
	case <-store.started:
	case <-ctx.Done():
		t.Fatal("background refresh did not start")
	}
	release.Do(func() { close(store.release) })
}

type tlsHintConcurrentAdmissionStore struct {
	OnDemandTLSStore
	remote *OnDemandTLSPolicy
}

func (s *tlsHintConcurrentAdmissionStore) ListCustomDomainTLSKnownHosts(ctx context.Context, after string, limit int) ([]string, error) {
	if after == "" {
		hosts := make([]string, limit)
		for n := range hosts[:limit-1] {
			hosts[n] = fmt.Sprintf("b%06d.example.com", n)
		}
		hosts[limit-1] = "shop.example.com"
		return hosts, nil
	}
	if allowed, decision := s.remote.Decide(ctx, "a.tenants.example.net"); !allowed {
		return nil, fmt.Errorf("remote admission: %s", decision)
	}
	return nil, nil
}

func (s *tlsHintConcurrentAdmissionStore) ListRecentCustomDomainTLSHosts(ctx context.Context, limit int) ([]string, error) {
	return s.OnDemandTLSStore.(state.CustomDomainTLSRecentHostReader).ListRecentCustomDomainTLSHosts(ctx, limit)
}

func TestOnDemandTLSKnownHintsCatchUpCrossEdgeAdmissionUnderLoad(t *testing.T) {
	s, other, _ := onDemandTLSFixture(t)
	other.SetNow(time.Now)
	store := &tlsHintConcurrentAdmissionStore{OnDemandTLSStore: s, remote: other}
	p := NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	// The second page admitted this hostname behind the inventory cursor.
	p.mu.Lock()
	p.tokens = 0
	p.refilled = time.Now().Add(time.Minute)
	p.mu.Unlock()
	if allowed, decision := p.Decide(t.Context(), "a.tenants.example.net"); !allowed {
		t.Fatalf("durably admitted remote hostname was throttled: %s", decision)
	}
}

type tlsHintStaleInventoryStore struct{ OnDemandTLSStore }

func (s *tlsHintStaleInventoryStore) ListCustomDomainTLSKnownHosts(context.Context, string, int) ([]string, error) {
	return []string{"shop.example.com"}, nil
}

func (s *tlsHintStaleInventoryStore) ListRecentCustomDomainTLSHosts(ctx context.Context, limit int) ([]string, error) {
	return s.OnDemandTLSStore.(state.CustomDomainTLSRecentHostReader).ListRecentCustomDomainTLSHosts(ctx, limit)
}

func TestOnDemandTLSKnownHintsPreserveCatchUpDuringScan(t *testing.T) {
	s, remote, _ := onDemandTLSFixture(t)
	remote.SetNow(time.Now)
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	if allowed, decision := remote.Decide(t.Context(), "a.tenants.example.net"); !allowed {
		t.Fatalf("remote admission: %s", decision)
	}
	if !p.catchUpRecentTLSHosts(t.Context(), "a.tenants.example.net") {
		t.Fatal("recent admissions were not loaded")
	}
	// Simulate a completed paginated scan that missed an insertion behind
	// its cursor: the catch-up set must survive replacement of the filter.
	p.mu.Lock()
	previous := p.hints
	p.hintsNextLoad = time.Time{}
	p.mu.Unlock()
	p.store = &tlsHintStaleInventoryStore{OnDemandTLSStore: s}
	if err := p.refreshKnownHostHints(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Refreshes run asynchronously; wait for the single flight to finish.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		refreshing := p.hintsRefreshing
		known := p.hints.contains("a.tenants.example.net")
		replaced := p.hints != previous
		p.mu.Unlock()
		if !refreshing {
			if !replaced {
				t.Fatal("inventory refresh did not replace the filter")
			}
			if !known {
				t.Fatal("replacement filter lost recent admission")
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("refresh did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}

type tlsRecentBlockingStore struct {
	OnDemandTLSStore
	started chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (s *tlsRecentBlockingStore) ListRecentCustomDomainTLSHosts(ctx context.Context, limit int) ([]string, error) {
	if s.reads.Add(1) == 1 {
		close(s.started)
	}
	select {
	case <-s.release:
		return s.OnDemandTLSStore.(state.CustomDomainTLSRecentHostReader).ListRecentCustomDomainTLSHosts(ctx, limit)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestOnDemandTLSRecentCatchUpSharesBoundedRead(t *testing.T) {
	s, _, _ := onDemandTLSFixture(t)
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	store := &tlsRecentBlockingStore{OnDemandTLSStore: s, started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(store.release) }) })
	p.store = store
	p.mu.Lock()
	p.tokens = 0
	p.refilled = time.Now().Add(time.Minute)
	p.mu.Unlock()
	const callers = 32
	var wg sync.WaitGroup
	for n := range callers {
		wg.Go(func() {
			if allowed, decision := p.Decide(t.Context(), fmt.Sprintf("unknown-%d.example.org", n)); allowed || decision != OnDemandTLSDenyOverload {
				t.Errorf("unknown host: allowed=%v decision=%s", allowed, decision)
			}
		})
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("recent catch-up did not start")
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		waiting := p.recentWaiters
		p.mu.Unlock()
		if waiting == callers {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("only %d callers joined the catch-up", waiting)
		case <-time.After(time.Millisecond):
		}
	}
	release.Do(func() { close(store.release) })
	wg.Wait()
	if got := store.reads.Load(); got > 2 {
		t.Fatalf("overload made %d recent-host reads, want at most 2", got)
	}
}

type tlsRecentSnapshotStore struct {
	OnDemandTLSStore
	started chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (s *tlsRecentSnapshotStore) ListRecentCustomDomainTLSHosts(ctx context.Context, limit int) ([]string, error) {
	hosts, err := s.OnDemandTLSStore.(state.CustomDomainTLSRecentHostReader).ListRecentCustomDomainTLSHosts(ctx, limit)
	if s.reads.Add(1) == 1 {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return hosts, err
}

func TestOnDemandTLSRecentCatchUpRetriesOlderSnapshot(t *testing.T) {
	s, remote, _ := onDemandTLSFixture(t)
	remote.SetNow(time.Now)
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	store := &tlsRecentSnapshotStore{OnDemandTLSStore: s, started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(store.release) }) })
	p.store = store
	p.mu.Lock()
	p.tokens = 0
	p.refilled = time.Now().Add(time.Minute)
	p.mu.Unlock()
	unknown := make(chan string, 1)
	go func() { _, decision := p.Decide(t.Context(), "unknown.example.org"); unknown <- decision }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("first snapshot did not start")
	}
	if allowed, decision := remote.Decide(t.Context(), "a.tenants.example.net"); !allowed {
		t.Fatalf("remote admission: %s", decision)
	}
	result := make(chan bool, 1)
	go func() { allowed, _ := p.Decide(t.Context(), "a.tenants.example.net"); result <- allowed }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		waiting := p.recentWaiters
		p.mu.Unlock()
		if waiting == 2 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("only %d requests joined the snapshot", waiting)
		case <-time.After(time.Millisecond):
		}
	}
	release.Do(func() { close(store.release) })
	select {
	case decision := <-unknown:
		if decision != OnDemandTLSDenyOverload {
			t.Fatalf("unknown host: %s", decision)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unknown host did not finish")
	}
	select {
	case allowed := <-result:
		if !allowed {
			t.Fatal("remote admission was lost behind an older ledger snapshot")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("newer ledger snapshot did not finish")
	}
	if got := store.reads.Load(); got != 2 {
		t.Fatalf("ledger reads = %d, want 2", got)
	}
}

type tlsRecentStaticStore struct {
	OnDemandTLSStore
	hosts []string
}

func (s *tlsRecentStaticStore) ListRecentCustomDomainTLSHosts(context.Context, int) ([]string, error) {
	return s.hosts, nil
}

func TestOnDemandTLSRecentHintsNeverGrantPermission(t *testing.T) {
	s, _, _ := onDemandTLSFixture(t)
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	p.store = &tlsRecentStaticStore{OnDemandTLSStore: s, hosts: []string{"pending.example.com", "unknown.example.com"}}
	p.mu.Lock()
	p.tokens = 0
	p.refilled = time.Now().Add(time.Minute)
	p.mu.Unlock()
	for host, want := range map[string]string{"pending.example.com": OnDemandTLSDenyUnverified, "unknown.example.com": OnDemandTLSDenyUnknown} {
		if allowed, decision := p.Decide(t.Context(), host); allowed || decision != want {
			t.Errorf("%s: allowed=%v decision=%s, want %s", host, allowed, decision, want)
		}
	}
}

func TestOnDemandTLSRecentCatchUpRejectsExcessWaiters(t *testing.T) {
	s, _, _ := onDemandTLSFixture(t)
	p := NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	if allowed, decision := p.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("warm inventory: %s", decision)
	}
	store := &tlsRecentBlockingStore{OnDemandTLSStore: s, started: make(chan struct{}), release: make(chan struct{})}
	p.store = store
	p.mu.Lock()
	p.tokens = 0
	p.refilled = time.Now().Add(time.Minute)
	p.recentWaiters = api.OnDemandTLSAskRecentHostWaiters
	p.mu.Unlock()
	if allowed, decision := p.Decide(t.Context(), "unknown.example.org"); allowed || decision != OnDemandTLSDenyOverload {
		t.Fatalf("excess waiter: allowed=%v decision=%s", allowed, decision)
	}
	if got := store.reads.Load(); got != 0 {
		t.Fatalf("excess waiter started %d reads", got)
	}
}

func TestOnDemandTLSKnownHintsShareWarmupAfterCallerCancellation(t *testing.T) {
	s, initial, _ := onDemandTLSFixture(t)
	if allowed, decision := initial.Decide(t.Context(), "shop.example.com"); !allowed {
		t.Fatalf("seed durable admission: %s", decision)
	}
	store := &tlsHintBlockingStore{OnDemandTLSStore: s, started: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-store.release:
		default:
			close(store.release)
		}
	}()
	p := NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	canceled := make(chan string, 1)
	go func() { _, decision := p.Decide(caller, "shop.example.com"); canceled <- decision }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("shared warm-up did not start")
	}
	cancel()
	select {
	case decision := <-canceled:
		if decision != OnDemandTLSDenyError {
			t.Fatalf("canceled caller: %s", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handshake waited for the shared read")
	}
	const callers = 16
	var ready sync.WaitGroup
	ready.Add(callers)
	results := make(chan bool, callers)
	for range callers {
		go func() {
			ready.Done()
			allowed, _ := p.Decide(t.Context(), "shop.example.com")
			results <- allowed
		}()
	}
	ready.Wait()
	close(store.release)
	for range callers {
		select {
		case allowed := <-results:
			if !allowed {
				t.Error("one caller's cancellation failed another's warm-up")
			}
		case <-time.After(time.Second):
			t.Fatal("shared warm-up did not complete")
		}
	}
	if got := store.reads.Load(); got != 1 {
		t.Fatalf("concurrent cold asks read the inventory %d times, want 1", got)
	}
}
