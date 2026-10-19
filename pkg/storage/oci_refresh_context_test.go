// adr: 054
package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOCIRefreshWaiterHonorsCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = io.WriteString(w, `{"token":"fresh-access","refresh_token":"fresh-grant","expires_in":3600}`)
	}))
	t.Cleanup(server.Close)
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	const scope = "repository:faas/apps:pull"
	leader := make(chan error, 1)
	go func() {
		_, err := backend.singleFlightRefresh(t.Context(), scope, "grant", server.URL, nil)
		leader <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	waiter := make(chan error, 1)
	go func() {
		_, err := backend.singleFlightRefresh(ctx, scope, "grant", server.URL, nil)
		waiter <- err
	}()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled waiter error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Error("canceled request waited for another caller's token refresh")
	}
	releaseOnce()
	if err := <-leader; err != nil {
		t.Fatalf("canceling a waiter disrupted the active refresh: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("token exchanges = %d, want one shared exchange", requests.Load())
	}
	if token, ok := backend.freshCachedAccessToken(scope); !ok || token != "fresh-access" {
		t.Fatalf("shared refresh result = %q, %v", token, ok)
	}
}
