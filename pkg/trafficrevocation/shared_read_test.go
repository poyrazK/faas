// adr: 570
package trafficrevocation

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatedStore holds every read until release closes, counting reads.
type gatedStore struct {
	reads   atomic.Int64
	release chan struct{}
}

func (s *gatedStore) Read(ctx context.Context, scopes []Scope) (map[Scope]State, error) {
	s.reads.Add(1)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result := make(map[Scope]State, len(scopes))
	for _, scope := range scopes {
		result[scope] = State{Revision: 1}
	}
	return result, nil
}

// PulseWatch F13: a burst after an app change admitted each request with its
// own store read through the gateway's eight-connection pool; reads past the
// 250 ms store timeout refused them as traffic_revocation_unavailable.
// Concurrent first admissions of one scope set now share a read.
func TestConcurrentAdmissionsShareOneStoreRead(t *testing.T) {
	store := &gatedStore{release: make(chan struct{})}
	r := New(store)
	scopes := []Scope{{Kind: "account", ID: "acct"}, {Kind: "app", ID: "app"}}
	const burst = 20
	var wg sync.WaitGroup
	errs := make(chan error, burst)
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, release, err := r.AdmitSnapshot(ctx, scopes, func(error) {})
			if err == nil {
				release()
			}
			errs <- err
		}()
	}
	deadline := time.Now().Add(time.Second)
	for store.reads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the rest of the burst join
	close(store.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("admission failed: %v", err)
		}
	}
	if n := store.reads.Load(); n != 1 {
		t.Fatalf("%d concurrent admissions made %d store reads, want 1", burst, n)
	}
}

// A preceding hop's baseline can be newer than a read already in flight, so
// AdmitAt never joins one.
func TestAdmitAtReadsAlongsideAnInFlightRead(t *testing.T) {
	store := &gatedStore{release: make(chan struct{})}
	r := New(store)
	scopes := []Scope{{Kind: "app", ID: "app"}}
	first := make(chan error, 1)
	go func() {
		_, release, err := r.AdmitSnapshot(context.Background(), scopes, func(error) {})
		if err == nil {
			release()
		}
		first <- err
	}()
	for store.reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	second := make(chan error, 1)
	go func() {
		release, err := r.AdmitAt(context.Background(), map[Scope]State{scopes[0]: {Revision: 1}}, func(error) {})
		if err == nil {
			release()
		}
		second <- err
	}()
	deadline := time.Now().Add(time.Second)
	for store.reads.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := store.reads.Load(); n != 2 {
		t.Fatalf("AdmitAt joined the in-flight read: %d reads, want 2", n)
	}
	close(store.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
