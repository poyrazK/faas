package operatorobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ADR-20261015154500000: disabled collection cannot stop retention; receipts
// and process-local metrics distinguish failure from missing observations.
type maintenanceTestStore struct {
	Store      state.Store
	pruneError error
	stop       context.CancelFunc
	failures   int
}

func (s *maintenanceTestStore) PruneOperatorAlertHistory(ctx context.Context, before time.Time, limit int) (int, error) {
	if s.pruneError != nil {
		return 0, s.pruneError
	}
	n, err := s.Store.PruneOperatorAlertHistory(ctx, before, limit)
	if s.stop != nil {
		s.stop()
	}
	return n, err
}
func (s *maintenanceTestStore) RecordOperatorAlertRetentionFailure(ctx context.Context, now time.Time) error {
	s.failures++
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.Store.RecordOperatorAlertRetentionFailure(ctx, now)
	if s.stop != nil {
		s.stop()
	}
	return err
}
func (s *maintenanceTestStore) OperatorAlertRetentionStatus(ctx context.Context, now time.Time) (state.OperatorAlertRetention, error) {
	return s.Store.OperatorAlertRetentionStatus(ctx, now)
}

func TestAlertMaintenanceRetentionRunsWithCollectionDisabled(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC()
	old := now.Add(-api.ObsAlertHistoryRetention - time.Hour)
	snapshot, err := alertSnapshot(historyRules(old, true), old)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	metrics := NewMaintenanceMetrics(reg)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	RunAlertCollector(context.Background(), store, nil, log, metrics)
	if n := testutil.ToFloat64(metrics.collectionEnabled); n != 0 {
		t.Fatalf("disabled gauge=%v", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &maintenanceTestStore{Store: store, stop: cancel}
	RunAlertRetention(ctx, fake, log, metrics)
	receipt, err := store.OperatorAlertRetentionStatus(context.Background(), now)
	if err != nil || receipt.ExpiredEpisodes != 0 || receipt.LastPrunedEpisodes != 1 || receipt.LastSuccessAt.IsZero() {
		t.Fatalf("cleanup did not run: %+v %v", receipt, err)
	}
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("retention", "success")); n != 1 {
		t.Fatalf("attempts=%v", n)
	}
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("collection", "success")); n != 0 {
		t.Fatalf("disabled collector ran: %v", n)
	}
	metrics.record("retention", now, errors.New("private detail"))
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("retention", "failure")); n != 1 {
		t.Fatalf("failure count=%v", n)
	}
	if n := testutil.ToFloat64(metrics.lastSuccess.WithLabelValues("retention")); n == 0 {
		t.Fatal("failure erased success timestamp")
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 5 {
		t.Fatalf("metrics=%d err=%v", len(families), err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "operation" && label.GetName() != "outcome" {
					t.Fatalf("unbounded label: %s", label.GetName())
				}
			}
		}
	}
}

func TestAlertMaintenanceFailedCleanupIsDurableAndCancellationStopsWrites(t *testing.T) {
	now := time.Now().UTC()
	store := state.NewMemStore()
	fake := &maintenanceTestStore{Store: store, pruneError: context.DeadlineExceeded}
	if err := RetainAlertsOnce(context.Background(), fake, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("failure=%v", err)
	}
	r, err := store.OperatorAlertRetentionStatus(context.Background(), now)
	if err != nil || !r.LastAttemptAt.Equal(now.Truncate(time.Microsecond)) || !r.LastSuccessAt.IsZero() || fake.failures != 1 {
		t.Fatalf("failed receipt=%+v err=%v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RetainAlertsOnce(ctx, fake, now); !errors.Is(err, context.Canceled) || fake.failures != 1 {
		t.Fatalf("shutdown wrote failure: %v calls=%d", err, fake.failures)
	}
}

func TestAlertMaintenanceHealthStates(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name    string
		c       state.OperatorAlertCollection
		enabled bool
		want    string
	}{
		{"disabled", state.OperatorAlertCollection{}, false, "disabled"},
		{"never", state.OperatorAlertCollection{}, true, "never_collected"},
		{"first failed", state.OperatorAlertCollection{LastAttemptAt: now}, true, "failed"},
		{"current", state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}, true, "current"},
		{"stale", state.OperatorAlertCollection{LastAttemptAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour)}, true, "stale"},
		{"failed", state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now.Add(-time.Minute)}, true, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := collectionHealth(tc.c, tc.enabled, now); got.CollectionState != tc.want {
				t.Fatalf("health=%+v", got)
			}
		})
	}
	for _, tc := range []struct {
		name string
		r    state.OperatorAlertRetention
		want string
	}{
		{"never", state.OperatorAlertRetention{}, "never_run"},
		{"first failed", state.OperatorAlertRetention{LastAttemptAt: now}, "failed"},
		{"current with backlog", state.OperatorAlertRetention{LastAttemptAt: now, LastSuccessAt: now, ExpiredEpisodes: 3}, "current"},
		{"stale", state.OperatorAlertRetention{LastAttemptAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour)}, "stale"},
		{"failed", state.OperatorAlertRetention{LastAttemptAt: now, LastSuccessAt: now.Add(-time.Minute)}, "failed"},
		{"future", state.OperatorAlertRetention{LastAttemptAt: now.Add(time.Hour), LastSuccessAt: now.Add(time.Hour)}, "unknown"},
	} {
		t.Run("retention "+tc.name, func(t *testing.T) {
			got := retentionHealth(tc.r, now)
			if got.State != tc.want || got.ExpiredEpisodes != tc.r.ExpiredEpisodes || tc.r.LastSuccessAt.IsZero() && got.LastPrunedEpisodes != nil {
				t.Fatalf("health=%+v", got)
			}
		})
	}
}
