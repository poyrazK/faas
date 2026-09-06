package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
