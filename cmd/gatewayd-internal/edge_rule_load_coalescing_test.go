package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type concurrentEdgeStore struct {
	calls atomic.Int32
	load  func(context.Context, string, int32) ([]state.EdgeRule, error)
}

func (s *concurrentEdgeStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error) {
	return s.load(ctx, host, s.calls.Add(1))
}
func (s *concurrentEdgeStore) GetCorsPresetByID(context.Context, string, string) (state.CorsPreset, error) {
	return state.CorsPreset{}, state.ErrNotFound
}
func newConcurrentEdgeMatcher(s *concurrentEdgeStore) *gatewaydEdgeRules {
	return newGatewaydEdgeRules(s, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
}

func TestEdgeLoadCoalescesConcurrentEmptyMisses(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s := &concurrentEdgeStore{load: func(ctx context.Context, _ string, _ int32) ([]state.EdgeRule, error) {
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	g := newConcurrentEdgeMatcher(s)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				g.MatchRoute(t.Context(), "a.example.com", "/", "GET")
			case 1:
				g.MatchRewrite(t.Context(), "a.example.com", "/", "GET")
			case 2:
				g.MatchRedirect(t.Context(), "a.example.com", "/", "GET")
			case 3:
				g.MatchHeaders(t.Context(), "a.example.com", "/", "GET")
			}
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // Hold the database read open while concurrent misses arrive.
	once.Do(func() { close(release) })
	wg.Wait()
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("100 concurrent misses made %d database reads, want 1", got)
	}
}

func TestEdgeLoadCanceledWaiterDoesNotCancelLeader(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := &concurrentEdgeStore{load: func(ctx context.Context, _ string, n int32) ([]state.EdgeRule, error) {
		if n == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	g := newConcurrentEdgeMatcher(s)
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() { close(release); <-finished })
	go func() { defer close(finished); _, err := g.loadHost(t.Context(), "a.example.com"); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := g.loadHost(ctx, "a.example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error=%v", err)
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("canceled waiter started database read: %d", got)
	}
	select {
	case err := <-done:
		t.Fatalf("leader ended before release: %v", err)
	default:
	}
}

func TestEdgeLoadCanceledLeaderAllowsRetry(t *testing.T) {
	entered := make(chan struct{})
	s := &concurrentEdgeStore{load: func(ctx context.Context, _ string, n int32) ([]state.EdgeRule, error) {
		if n == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}}
	g := newConcurrentEdgeMatcher(s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	leader := make(chan error, 1)
	go func() { _, err := g.loadHost(ctx, "a.example.com"); leader <- err }()
	<-entered
	follower := make(chan error, 1)
	go func() { _, err := g.loadHost(t.Context(), "a.example.com"); follower <- err }()
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error=%v", err)
	}
	select {
	case err := <-follower:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follower stuck after leader cancellation")
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("reads=%d, want canceled read plus retry", got)
	}
}

func TestEdgeLoadResetAndOtherHostsDoNotWaitForOldRead(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s := &concurrentEdgeStore{load: func(ctx context.Context, _ string, n int32) ([]state.EdgeRule, error) {
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, nil
	}}
	g := newConcurrentEdgeMatcher(s)
	done := make(chan error, 1)
	go func() { _, err := g.loadHost(t.Context(), "a.example.com"); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := g.loadHost(ctx, "b.example.com"); err != nil {
		t.Fatalf("unrelated host blocked: %v", err)
	}
	g.Reset()
	if _, err := g.loadHost(ctx, "a.example.com"); err != nil {
		t.Fatalf("new generation blocked: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := g.loadHost(ctx, "a.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := s.calls.Load(); got != 3 {
		t.Fatalf("reads=%d, want old generation, other host, new generation", got)
	}
}

// A deny rule must keep applying through a Postgres outage: once the cached
// entry expires, a failed reload serves the last-known rules instead of
// failing open, and retries are spaced by the backoff instead of hitting the
// database on every per-kind lookup of every request.
func TestEdgeLoadServesLastKnownRulesWhileStoreIsDown(t *testing.T) {
	var down atomic.Bool
	s := &concurrentEdgeStore{load: func(_ context.Context, host string, _ int32) ([]state.EdgeRule, error) {
		if down.Load() {
			return nil, errors.New("postgres unavailable")
		}
		return []state.EdgeRule{{
			ID: "deny-office", AccountID: "acc", AppID: "app", MatchHost: host,
			Enabled: true, Kind: state.EdgeRuleKindIP,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"192.0.2.0/24"}}},
		}}, nil
	}}
	g := newConcurrentEdgeMatcher(s)
	now := time.Unix(1_000, 0)
	clock := func() time.Time { return now }
	g.clock = clock
	g.cache.SetClock(clock)

	if rule := g.MatchIP(t.Context(), "a.example.com", "/", "GET"); rule == nil {
		t.Fatal("initial load did not compile the deny rule")
	}
	down.Store(true)
	now = now.Add(time.Hour) // far past the cache TTL
	before := s.calls.Load()
	for range 20 {
		if rule := g.MatchIP(t.Context(), "a.example.com", "/", "GET"); rule == nil || rule.ID != "deny-office" {
			t.Fatalf("MatchIP during outage = %v; want the last-known deny rule", rule)
		}
		if rule := g.MatchMaintenance(t.Context(), "a.example.com", "/", "GET"); rule != nil {
			t.Fatalf("MatchMaintenance = %v; want none (host has no maintenance rule)", rule)
		}
	}
	if got := s.calls.Load() - before; got != 1 {
		t.Fatalf("store reads during backoff = %d, want 1", got)
	}

	// After the backoff one request retries; recovery replaces the fallback.
	down.Store(false)
	now = now.Add(edgeRuleLoadRetryBackoff)
	if rule := g.MatchIP(t.Context(), "a.example.com", "/", "GET"); rule == nil {
		t.Fatal("recovered load lost the deny rule")
	}
	if got := s.calls.Load() - before; got != 2 {
		t.Fatalf("store reads after backoff = %d, want 2", got)
	}
}

// A host the gateway never loaded has no last-known set: the loader error
// stands (each kind keeps its posture), but retries are still backed off.
func TestEdgeLoadBacksOffColdHostFailures(t *testing.T) {
	s := &concurrentEdgeStore{load: func(context.Context, string, int32) ([]state.EdgeRule, error) {
		return nil, errors.New("postgres unavailable")
	}}
	g := newConcurrentEdgeMatcher(s)
	now := time.Unix(1_000, 0)
	g.clock = func() time.Time { return now }
	for range 10 {
		if rule := g.MatchJWT(t.Context(), "cold.example.com", "/", "GET"); rule == nil || !rule.Unavailable {
			t.Fatalf("MatchJWT on a cold failing host = %v; want fail-closed Unavailable", rule)
		}
		g.MatchIP(t.Context(), "cold.example.com", "/", "GET")
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("store reads = %d, want 1 inside the backoff window", got)
	}
}

// The loader drops rules already past expires_at (clock-skew guard behind the
// store filter) and records the earliest future expiry so the compiled set is
// reloaded the moment a time-boxed rule lapses.
func TestActiveEdgeRulesDropsExpiredAndRecordsEarliestExpiry(t *testing.T) {
	now := time.Unix(1_000, 0)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	rules := []state.EdgeRule{
		{ID: "lapsed", ExpiresAt: at(-time.Second)},
		{ID: "later", ExpiresAt: at(time.Hour)},
		{ID: "forever"},
		{ID: "soon", ExpiresAt: at(time.Minute)},
	}
	active, notAfter := activeEdgeRules(rules, now)
	var ids []string
	for _, r := range active {
		ids = append(ids, r.ID)
	}
	if want := []string{"later", "forever", "soon"}; !slices.Equal(ids, want) {
		t.Fatalf("active = %v, want %v", ids, want)
	}
	if !notAfter.Equal(now.Add(time.Minute)) {
		t.Fatalf("notAfter = %v, want the earliest future expiry", notAfter)
	}
	if len(rules) != 4 || rules[0].ID != "lapsed" {
		t.Fatal("activeEdgeRules mutated its input")
	}
}
