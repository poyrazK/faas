package logsearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// blockingEvents holds every read until the test releases it.
type blockingEvents struct {
	entered chan string
	release chan struct{}
	err     error
}

func (b *blockingEvents) InsertLogEvent(_ context.Context, event state.LogEvent) (state.LogEvent, error) {
	return event, nil
}

func (b *blockingEvents) ListLogEvents(_ context.Context, filter state.LogEventFilter) ([]state.LogEvent, bool, error) {
	return nil, false, b.read(filter.AccountID)
}

func (b *blockingEvents) ListAccountTraceLogEvents(_ context.Context, filter state.AccountTraceLogFilter) ([]state.LogEvent, bool, error) {
	return nil, false, b.read(filter.AccountID)
}

func (b *blockingEvents) FacetLogEvents(_ context.Context, filter state.LogEventFilter, _ int) (state.LogEventFacets, error) {
	return state.LogEventFacets{}, b.read(filter.AccountID)
}

func (b *blockingEvents) read(account string) error {
	if b.err != nil {
		return b.err
	}
	b.entered <- account
	<-b.release
	return nil
}

// Each ledger read holds a database connection for up to the statement
// timeout: apid admits two of one account's reads and three in all here,
// refuses a read that finds no slot in time, and hands a freed slot to the
// next read.
func TestReadLimiterBoundsLogReadsPerAccountAndInAll(t *testing.T) {
	inner := &blockingEvents{entered: make(chan string, 8), release: make(chan struct{})}
	events := NewReadLimiter(2, 3, 100*time.Millisecond).Limit(inner)
	ctx := context.Background()
	done := make(chan error, 8)
	for _, account := range []string{"a", "a", "b"} {
		go func() {
			_, _, err := events.ListLogEvents(ctx, state.LogEventFilter{AccountID: account})
			done <- err
		}()
		<-inner.entered
	}
	if _, _, err := events.ListLogEvents(ctx, state.LogEventFilter{AccountID: "a"}); !errors.Is(err, state.ErrLogReadBusy) {
		t.Fatalf("a third read of one account = %v, want busy", err)
	}
	if _, err := events.FacetLogEvents(ctx, state.LogEventFilter{AccountID: "c"}, 1); !errors.Is(err, state.ErrLogReadBusy) {
		t.Fatalf("a read past the total = %v, want busy", err)
	}
	inner.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _, err := events.ListAccountTraceLogEvents(ctx, state.AccountTraceLogFilter{AccountID: "c"})
		done <- err
	}()
	if got := <-inner.entered; got != "c" {
		t.Fatalf("the freed slot went to %q", got)
	}
	close(inner.release)
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := events.ListLogEvents(ctx, state.LogEventFilter{AccountID: "a"}); err != nil {
		t.Fatalf("a read after every slot freed = %v", err)
	}
}

type busyHost struct{ fakeHost }

func (busyHost) LogEvents() state.LogEventStore {
	return &blockingEvents{err: state.ErrLogReadBusy}
}

func TestSearchRefusesABusyReadWithRetryAfter(t *testing.T) {
	svc, acct := newService(t, true)
	svc.Host = busyHost{svc.Host.(fakeHost)}
	for _, h := range []func(http.ResponseWriter, *http.Request, state.Account){svc.Search, svc.Facets} {
		r := httptest.NewRequest(http.MethodGet, "/v1/apps/shop/log-events", nil)
		r.SetPathValue("slug", "shop")
		w := httptest.NewRecorder()
		h(w, r, acct)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("busy read = %d %s", w.Code, w.Body.String())
		}
		var problem api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || problem.Code != "log_reads_busy" || problem.Limit == nil ||
			*problem.Limit != api.LogReadsInFlightPerAccount {
			t.Fatalf("problem = %+v, %v", problem, err)
		}
	}
}
