package objectstorage_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type versioningReadStore struct {
	state.ObjectBucketVersioningStore
	current  state.ObjectBucketVersioning
	observed []string
}

func (s *versioningReadStore) GetObjectBucketVersioning(context.Context, string, string, string) (state.ObjectBucketVersioning, error) {
	return s.current, nil
}

func (s *versioningReadStore) ObserveObjectBucketVersioning(_ context.Context, _, _, _, status string) (state.ObjectBucketVersioning, error) {
	s.observed = append(s.observed, status)
	j := s.current
	j.ObservedStatus = status
	return j, nil
}

type versioningReadProvider struct{ status string }

func (p versioningReadProvider) GetBucketVersioning(context.Context, string) (objectstorage.BucketVersioning, error) {
	return objectstorage.BucketVersioning{Status: p.status}, nil
}

func (versioningReadProvider) PutBucketVersioning(context.Context, string, string) error { return nil }

// ChronoLake finding 12: `gregale bucket versioning status` on a
// never-versioned bucket recorded a revision and started a capacity inventory
// that fenced writes. Reading that baseline must not write.
func TestBucketVersioningReadOfBaselineIsReadOnly(t *testing.T) {
	ready := state.ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: "b", State: "ready"}}
	b := state.ObjectBucket{ID: "b", AccountID: "a", AppID: "app", PhysicalName: "phys"}

	store := &versioningReadStore{current: ready}
	j, v, err := objectstorage.BucketVersioningService{Store: store, Provider: versioningReadProvider{status: "Suspended"}}.Read(t.Context(), b)
	if err != nil || len(store.observed) != 0 || j.ObservedStatus != "Suspended" || j.State != "ready" || j.Revision != 0 || v.Status != "Suspended" {
		t.Fatalf("baseline read wrote state: job=%+v observed=%v err=%v", j, store.observed, err)
	}

	// Any other status, or a bucket Gregale already changed, is still recorded.
	for _, tc := range []struct {
		name    string
		current state.ObjectBucketVersioning
		status  string
	}{
		{"enabled", ready, "Enabled"},
		{"changed", state.ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: "b", State: "ready", DesiredStatus: "Enabled", ObservedStatus: "Enabled", Revision: 1}}, "Suspended"},
	} {
		store := &versioningReadStore{current: tc.current}
		if _, _, err := (objectstorage.BucketVersioningService{Store: store, Provider: versioningReadProvider{status: tc.status}}).Read(t.Context(), b); err != nil || len(store.observed) != 1 {
			t.Fatalf("%s: observation skipped: %v %v", tc.name, store.observed, err)
		}
	}
}
