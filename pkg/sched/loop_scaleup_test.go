package sched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched/scaleup"
	"github.com/onebox-faas/faas/pkg/state"
)

type scaleupLoopTestStore struct{}

func (scaleupLoopTestStore) ListAllApps(context.Context) ([]state.App, error) {
	return nil, nil
}

func (scaleupLoopTestStore) ListAppsByNodeID(context.Context, string) ([]state.App, error) {
	return nil, nil
}

type blockingScaleupScraper struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (s *blockingScaleupScraper) Scrape(ctx context.Context) (map[string]int64, error) {
	s.mu.Lock()
	s.calls++
	if s.calls == 1 {
		close(s.started)
	}
	s.mu.Unlock()
	defer close(s.finished)
	select {
	case <-s.release:
		return map[string]int64{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingScaleupScraper) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestLoopRunScaleUpDoesNotBlockOnScrape(t *testing.T) {
	scraper := &blockingScaleupScraper{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	trigger := scaleup.New(scaleupLoopTestStore{}, nil, scraper, nil, nil, scaleup.Options{})
	loop := NewLoop(nil, nil, testLog()).WithScaleUp(trigger)

	returned := make(chan struct{})
	go func() {
		loop.runScaleUp(context.Background())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runScaleUp blocked on the metrics scrape")
	}

	select {
	case <-scraper.started:
	case <-time.After(time.Second):
		t.Fatal("scale-up tick did not start")
	}
	loop.runScaleUp(context.Background())
	if got := scraper.callCount(); got != 1 {
		t.Fatalf("scrape calls while first tick blocked = %d, want 1", got)
	}

	close(scraper.release)
	<-scraper.finished
}
