package healthdispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dispatchJob struct{ ID string }
type dispatchFixture struct {
	mu                         sync.Mutex
	queue                      []dispatchJob
	active, maxActive          int
	recoveryClaims, fairClaims int
}

func (s *dispatchFixture) ClaimNextProjectEnvironmentPromotionHealth(ctx context.Context, _ string, prefer bool) (*dispatchJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, nil
	}
	job := s.queue[0]
	s.queue = s.queue[1:]
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	if prefer {
		s.recoveryClaims++
	} else {
		s.fairClaims++
	}
	return &job, nil
}
func (*dispatchFixture) ProjectEnvironmentPromotionHealthLeaseCurrent(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func TestHealthDispatchSlowJobAndCancellation(t *testing.T) {
	const count = 61
	scheduler := &dispatchFixture{}
	for i := range count {
		scheduler.queue = append(scheduler.queue, dispatchJob{ID: fmt.Sprint(i)})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstStarted := make(chan struct{})
	othersDone := make(chan struct{})
	finished := make(chan struct{})
	reportSeen := make(chan struct{}, 1)
	var completed, reported atomic.Int32
	reconcile := func(ctx context.Context, job dispatchJob, _ string) error {
		defer func() { scheduler.mu.Lock(); scheduler.active--; scheduler.mu.Unlock() }()
		if job.ID == "0" {
			close(firstStarted)
			<-ctx.Done()
			return ctx.Err()
		}
		if completed.Add(1) == count-1 {
			close(othersDone)
		}
		if job.ID == "1" {
			return errors.New("probe unavailable")
		}
		return nil
	}
	go func() {
		Run(ctx, Options{Workers: 4, PollInterval: time.Millisecond, ClaimTimeout: time.Second, JobTimeout: time.Second}, scheduler.ClaimNextProjectEnvironmentPromotionHealth, reconcile, func(_ *dispatchJob, _ error) { reported.Add(1); reportSeen <- struct{}{} })
		close(finished)
	}()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("slow job did not start")
	}
	select {
	case <-othersDone:
	case <-time.After(5 * time.Second):
		t.Fatal("slow job blocked jobs beyond the old batch")
	}
	select {
	case <-reportSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("job error was not reported")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation left workers running")
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.active != 0 || scheduler.maxActive > 4 || scheduler.maxActive < 2 {
		t.Fatal("concurrency was not bounded", scheduler.active, scheduler.maxActive)
	}
	if reported.Load() != 1 {
		t.Fatal("job error was not isolated/reported", reported.Load())
	}
}
