// adr: 20261023223000

package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// busyDeletionStore refuses the first busy admissions as ErrObjectBucketBusy
// and then returns final.
type busyDeletionStore struct {
	state.ObjectDeletionStore
	busy, calls int
	final       error
	tokens      map[string]bool
}

func (s *busyDeletionStore) BeginObjectDeletion(_ context.Context, j state.ObjectDeletion, _ api.ObjectStoragePolicy) (state.ObjectDeletion, bool, error) {
	s.calls++
	s.tokens[j.Token] = true
	if s.calls <= s.busy {
		return state.ObjectDeletion{}, false, errors.Join(state.ErrConflict, state.ErrObjectBucketBusy)
	}
	return j, s.final == nil, s.final
}

func TestDeletionAdmissionWaitsOutBusyBuckets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		busy      int
		final     error
		wait      time.Duration
		wantCalls int
		want      error
	}{
		{name: "queued behind other deletions", busy: 3, wait: time.Minute, wantCalls: 4},
		{name: "plain conflict returns at once", final: state.ErrConflict, wait: time.Minute, wantCalls: 1, want: state.ErrConflict},
		{name: "busy past the wait", busy: 1 << 20, wait: 300 * time.Millisecond, want: state.ErrObjectBucketBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &busyDeletionStore{busy: tc.busy, final: tc.final, tokens: map[string]bool{}}
			s := DeletionService{Store: store, admissionWait: tc.wait}
			started := time.Now()
			_, created, err := s.begin(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString()}}, api.ObjectStoragePolicy{})
			if !errors.Is(err, tc.want) || (tc.want == nil) != created {
				t.Fatalf("err = %v created = %v, want %v", err, created, tc.want)
			}
			if tc.wantCalls != 0 && store.calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", store.calls, tc.wantCalls)
			}
			if len(store.tokens) != store.calls {
				t.Fatal("an attempt reused a lease token")
			}
			if elapsed := time.Since(started); elapsed > tc.wait+time.Second {
				t.Fatalf("waited %v past the %v bound", elapsed, tc.wait)
			}
		})
	}
}
