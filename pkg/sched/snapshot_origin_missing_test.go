package sched_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

type originMissingStore struct {
	ids    []string
	grace  time.Duration
	failOn string
	marked []string
}

func (s *originMissingStore) SnapshotsMissingFromOrigin(_ context.Context, grace time.Duration, _ int) ([]string, error) {
	s.grace = grace
	return s.ids, nil
}

func (s *originMissingStore) MarkSnapshotStale(_ context.Context, id string) error {
	if id == s.failOn {
		return errors.New("boom")
	}
	s.marked = append(s.marked, id)
	return nil
}

// Prod hunt r72: snapshots whose storage object is gone are marked stale, one
// failure does not stop the rest, and the grace is the documented hour.
func TestRetireSnapshotsMissingFromOrigin(t *testing.T) {
	store := &originMissingStore{ids: []string{"a", "b", "c"}, failOn: "b"}
	n, err := sched.RetireSnapshotsMissingFromOrigin(context.Background(), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n != 2 || !reflect.DeepEqual(store.marked, []string{"a", "c"}) {
		t.Fatalf("marked %d %v, want a and c", n, store.marked)
	}
	if store.grace != time.Hour {
		t.Fatalf("grace = %v, want 1h", store.grace)
	}
}
