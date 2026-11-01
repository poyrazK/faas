package gateway

// adr: 20261027085556 — amendment 2: a host policy lookup keeps the host read
// bound for its first attempt and retries a failure once within the cold bound.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type coldHostPolicyBackend struct {
	*fakeBackend
	mu      sync.Mutex
	calls   int
	attempt func(ctx context.Context, call int) (App, bool, error)
}

func (b *coldHostPolicyBackend) LookupHostPolicy(ctx context.Context, _ string) (App, bool, error) {
	b.mu.Lock()
	b.calls++
	call := b.calls
	b.mu.Unlock()
	return b.attempt(ctx, call)
}

func (b *coldHostPolicyBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func coldHostPolicyFixture(t *testing.T, attempt func(context.Context, int) (App, bool, error)) (*Handler, *coldHostPolicyBackend, *http.Request) {
	t.Helper()
	h, base, _ := newTestHandler(t)
	backend := &coldHostPolicyBackend{fakeBackend: base, attempt: attempt}
	h.backend = backend
	return h, backend, httptest.NewRequest(http.MethodGet, "http://"+base.host+"/", nil)
}

func TestHostPolicyLookupRetriesAFailedAttempt(t *testing.T) {
	want := App{ID: "app"}
	h, backend, r := coldHostPolicyFixture(t, func(_ context.Context, call int) (App, bool, error) {
		if call == 1 {
			return App{}, false, errors.New("acquire connection: pool exhausted")
		}
		return want, true, nil
	})
	app, found, err := h.lookupAppPolicy(r, "app.example")
	if err != nil || !found || app.ID != want.ID {
		t.Fatalf("lookup = %+v found=%t err=%v; want the retry's app", app, found, err)
	}
	if backend.callCount() != 2 {
		t.Fatalf("lookups = %d; want the failed attempt and one retry", backend.callCount())
	}
}

// The first attempt of a stalled read ends at the host read bound and the
// retry gets the rest of the cold bound.
func TestHostPolicyLookupRetriesASlowFirstAttempt(t *testing.T) {
	want := App{ID: "app"}
	h, backend, r := coldHostPolicyFixture(t, func(ctx context.Context, call int) (App, bool, error) {
		if call == 1 {
			<-ctx.Done()
			return want, true, nil // An answer after expiry must not count.
		}
		return want, true, nil
	})
	started := time.Now()
	app, found, err := h.lookupAppPolicy(r, "app.example")
	if elapsed := time.Since(started); elapsed < api.TrafficPublicHostReadTimeout {
		t.Fatalf("lookup finished after %v; want the first attempt to run to its bound", elapsed)
	}
	if err != nil || !found || app.ID != want.ID || backend.callCount() != 2 {
		t.Fatalf("lookup = %+v found=%t err=%v lookups=%d; want the retry's app", app, found, err, backend.callCount())
	}
}

func TestHostPolicyLookupGivesUpAtTheColdBound(t *testing.T) {
	h, backend, r := coldHostPolicyFixture(t, func(ctx context.Context, _ int) (App, bool, error) {
		<-ctx.Done()
		return App{ID: "late"}, true, nil
	})
	started := time.Now()
	_, found, err := h.lookupAppPolicy(r, "app.example")
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) || found || elapsed < api.TrafficPublicHostColdReadTimeout || elapsed > api.TrafficPublicHostColdReadTimeout+time.Second {
		t.Fatalf("stalled store: found=%t err=%v after %v; want a deadline at the cold bound", found, err, elapsed)
	}
	if backend.callCount() != 2 {
		t.Fatalf("lookups = %d; want the first attempt and one retry", backend.callCount())
	}
}

func TestHostPolicyLookupDoesNotRetryAnUnknownHost(t *testing.T) {
	h, backend, r := coldHostPolicyFixture(t, func(context.Context, int) (App, bool, error) {
		return App{}, false, nil
	})
	if _, found, err := h.lookupAppPolicy(r, "missing.example"); err != nil || found {
		t.Fatalf("unknown host: found=%t err=%v", found, err)
	}
	if backend.callCount() != 1 {
		t.Fatalf("lookups = %d; an unknown host is a store answer, not a failure", backend.callCount())
	}
}
