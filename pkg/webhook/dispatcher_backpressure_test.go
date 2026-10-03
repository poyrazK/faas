package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type recordingClaimStore struct {
	state.Store
	mu       sync.Mutex
	limits   []int
	failOnce bool
}

func (s *recordingClaimStore) ClaimDueAppWebhookDeliveries(ctx context.Context, limit int, now time.Time) ([]state.AppWebhookDelivery, error) {
	s.mu.Lock()
	s.limits = append(s.limits, limit)
	fail := s.failOnce
	s.failOnce = false
	s.mu.Unlock()
	if fail {
		return nil, errors.New("claim unavailable")
	}
	return s.Store.ClaimDueAppWebhookDeliveries(ctx, limit, now)
}

func (s *recordingClaimStore) claimLimits() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.limits...)
}

func TestDispatcherBoundsInFlightAcrossTicks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	var active, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	m := state.NewMemStore()
	loader, sealed := identityForSealedBlob(t)
	if _, err := m.CreateApp(ctx, state.App{ID: "app", AccountID: "account", Slug: "backpressure", Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	hook := newTestAppWebhook(t, m, "app", "account", srv.URL, state.AppWebhookRetryDefault)
	if _, err := m.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		d, err := m.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID: hook.ID, AppID: "app", AccountID: "account",
			Event: "cron.fired", Payload: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	store := &recordingClaimStore{Store: m}
	metrics := NewDeliveryHealthMetrics(prometheus.NewRegistry(), "schedd")
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithMaxInFlight(2)
	d.IdentityLoader = loader
	d.HTTPClient = srv.Client()
	d.HealthMetrics = metrics
	d.cycle(ctx)
	waitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("receiver did not start before deadline")
		}
	}
	waitStarted()
	waitStarted()
	if got := testutil.ToFloat64(metrics.inFlight); got != 2 {
		t.Fatalf("in-flight = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metrics.saturated); got != 1 {
		t.Fatalf("saturated = %v, want 1", got)
	}
	d.cycle(ctx)
	if limits := store.claimLimits(); len(limits) != 1 || limits[0] != 2 {
		t.Fatalf("claim limits while saturated = %v, want [2]", limits)
	}
	var pending int
	for _, id := range ids {
		row, err := m.AppWebhookDeliveryByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == state.AppWebhookDeliveryPending && row.Attempt == 0 {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("pending unclaimed deliveries = %d, want 1", pending)
	}

	release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(metrics.inFlight) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := testutil.ToFloat64(metrics.inFlight); got != 1 {
		t.Fatalf("in-flight after one completion = %v, want 1", got)
	}
	d.cycle(ctx)
	if limits := store.claimLimits(); len(limits) != 2 || limits[1] != 1 {
		t.Fatalf("claim limits after one slot freed = %v, want [2 1]", limits)
	}
	waitStarted()
	release <- struct{}{}
	release <- struct{}{}
	done := make(chan struct{})
	go func() { d.inflight.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("delivery workers did not finish before deadline")
	}
	for _, id := range ids {
		row, err := m.AppWebhookDeliveryByID(ctx, id)
		if err != nil || row.Status != state.AppWebhookDeliverySucceeded {
			t.Fatalf("delivery %s = %s, %v; want succeeded", id, row.Status, err)
		}
	}
	if got := peak.Load(); got > 2 {
		t.Fatalf("peak receiver concurrency = %d, want <= 2", got)
	}
	if got := testutil.ToFloat64(metrics.inFlight); got != 0 {
		t.Fatalf("final in-flight = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.saturated); got != 0 {
		t.Fatalf("final saturated = %v, want 0", got)
	}
}

func TestDispatcherClaimFailureReleasesCapacity(t *testing.T) {
	store := &recordingClaimStore{Store: state.NewMemStore(), failOnce: true}
	metrics := NewDeliveryHealthMetrics(prometheus.NewRegistry(), "schedd")
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithMaxInFlight(2)
	d.HealthMetrics = metrics
	d.cycle(context.Background())
	if got := testutil.ToFloat64(metrics.saturated); got != 0 {
		t.Fatalf("saturated after failed claim = %v, want 0", got)
	}
	d.cycle(context.Background())
	if limits := store.claimLimits(); len(limits) != 2 || limits[0] != 2 || limits[1] != 2 {
		t.Fatalf("claim limits after failure = %v, want [2 2]", limits)
	}
}
