package objectgc_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/objectgc"
	"github.com/onebox-faas/faas/pkg/state"
)

type sweepLister map[string][]string

func (l sweepLister) List(_ context.Context, prefix string) ([]string, error) {
	return l[prefix], nil
}

type sweepStore struct {
	jobs      map[string]state.Job
	builds    map[string]state.Build
	requested []string
}

func (s *sweepStore) JobGetByID(_ context.Context, id string) (state.Job, error) {
	if j, ok := s.jobs[id]; ok {
		return j, nil
	}
	return state.Job{}, state.ErrNotFound
}

func (s *sweepStore) BuildByID(_ context.Context, id string) (state.Build, error) {
	if b, ok := s.builds[id]; ok {
		return b, nil
	}
	return state.Build{}, state.ErrNotFound
}

func (s *sweepStore) RequestStorageObjectDeletion(_ context.Context, key, reason string) error {
	if !strings.HasPrefix(reason, "objectgc sweep: ") {
		panic("reason " + reason)
	}
	s.requested = append(s.requested, key)
	return nil
}

func uuidv7At(t *testing.T, at time.Time) string {
	t.Helper()
	id, err := uuid.NewV7FromReader(strings.NewReader(strings.Repeat("x", 64)))
	if err != nil {
		t.Fatal(err)
	}
	ms := uint64(at.UnixMilli())
	for i := 0; i < 6; i++ {
		id[i] = byte(ms >> (40 - 8*i))
	}
	return id.String()
}

func TestSweep_RequestsOnlyGarbage(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	job := func() string { return uuid.NewString() }
	gone, failed, ready, pending := job(), job(), job(), job()
	attempt := func(j string) string { return "jobs/" + j + "__" + uuid.NewString() + ".ext4" }
	readyKey := attempt(ready)
	staleReady := attempt(ready)
	old := now.Add(-48 * time.Hour)
	srcGone, srcDone, srcRunning := uuidv7At(t, old), uuidv7At(t, old), uuidv7At(t, old)
	srcYoung := uuidv7At(t, now.Add(-time.Hour))
	src := func(id string) string { return "sources/" + id + ".tar.gz" }

	lister := sweepLister{
		"jobs/": {
			attempt(gone), attempt(failed), readyKey, staleReady, attempt(pending),
			"jobs/not-a-job.ext4",
		},
		"sources/": {
			src(srcGone), src(srcDone), src(srcRunning), src(srcYoung),
			"sources/" + uuid.NewString() + ".tar.gz", // v4: no creation time
		},
	}
	st := &sweepStore{
		jobs: map[string]state.Job{
			failed:  {ImageMaterializationStatus: "failed"},
			ready:   {ImageMaterializationStatus: "ready", ImageStorageKey: readyKey},
			pending: {ImageMaterializationStatus: "pending"},
		},
		builds: map[string]state.Build{
			srcDone:    {Status: state.BuildSucceeded},
			srcRunning: {Status: state.BuildRunning},
		},
	}
	n, err := objectgc.Sweep(context.Background(), lister, st, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{lister["jobs/"][0], lister["jobs/"][1], staleReady, src(srcGone), src(srcDone)}
	slices.Sort(want)
	slices.Sort(st.requested)
	if n != len(want) || !slices.Equal(st.requested, want) {
		t.Fatalf("requested %d %v, want %v", n, st.requested, want)
	}
}
