package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// gatedPublicReadStore holds every AppBySlug until release is closed and
// counts the store reads the matcher makes.
type gatedPublicReadStore struct {
	PublicReadStore
	started     chan struct{}
	release     chan struct{}
	appBySlug   atomic.Int32
	listBuckets atomic.Int32
}

func newGatedPublicReadStore(t *testing.T) *gatedPublicReadStore {
	return &gatedPublicReadStore{PublicReadStore: seedForwardBucket(t), started: make(chan struct{}, 64), release: make(chan struct{})}
}

func (s *gatedPublicReadStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	s.appBySlug.Add(1)
	s.started <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		return state.App{}, ctx.Err()
	}
	return s.PublicReadStore.AppBySlug(ctx, slug)
}

func (s *gatedPublicReadStore) ListObjectBuckets(ctx context.Context, accountID, appID string) ([]state.ObjectBucket, error) {
	s.listBuckets.Add(1)
	return s.PublicReadStore.ListObjectBuckets(ctx, accountID, appID)
}

func sharedLookupMatcher(t *testing.T, store PublicReadStore) *publicReadHandler {
	t.Helper()
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h.(*publicReadForwarder).match
}

// TestPublicReadConcurrentMissesShareOneLookup: requests that miss a host's
// mounts together read the store once, not once each.
func TestPublicReadConcurrentMissesShareOneLookup(t *testing.T) {
	store := newGatedPublicReadStore(t)
	match := sharedLookupMatcher(t, store)
	const callers = 32
	var wg sync.WaitGroup
	results := make(chan int, callers)
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buckets, err := match.hostMounts(t.Context(), "assets-app.gregale.dev")
			if err != nil {
				errs <- err
				return
			}
			results <- len(buckets)
		}()
	}
	<-store.started
	// Give the other callers time to arrive while the first lookup is held.
	time.Sleep(50 * time.Millisecond)
	close(store.release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for n := range results {
		if n != 1 {
			t.Fatalf("a caller got %d mounts, want 1", n)
		}
	}
	if got, want := store.appBySlug.Load()+store.listBuckets.Load(), int32(2); got != want {
		t.Fatalf("%d concurrent misses made %d store reads, want %d", callers, got, want)
	}
}

// TestPublicReadSharedLookupOutlivesItsCaller: a caller leaves at its own
// deadline, and its cancellation does not fail the shared lookup the other
// callers wait on.
func TestPublicReadSharedLookupOutlivesItsCaller(t *testing.T) {
	store := newGatedPublicReadStore(t)
	match := sharedLookupMatcher(t, store)
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leader := make(chan error, 1)
	go func() {
		_, err := match.hostMounts(leaderCtx, "assets-app.gregale.dev")
		leader <- err
	}()
	<-store.started

	waiter := make(chan error, 1)
	go func() {
		buckets, err := match.hostMounts(t.Context(), "assets-app.gregale.dev")
		if err == nil && len(buckets) != 1 {
			err = errors.New("the waiting caller got no mount")
		}
		waiter <- err
	}()
	impatient, cancelImpatient := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelImpatient()
	if _, err := match.hostMounts(impatient, "assets-app.gregale.dev"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a caller past its deadline got %v, want its own deadline", err)
	}

	cancelLeader()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled leader got %v, want its cancellation", err)
	}
	close(store.release)
	if err := <-waiter; err != nil {
		t.Fatalf("the leader's cancellation failed the shared lookup: %v", err)
	}
	if got := store.appBySlug.Load(); got != 1 {
		t.Fatalf("store read %d times, want 1", got)
	}
}
