package main_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioObserversUseProvisionedConsumerCredentials(t *testing.T) {
	readers := []struct {
		name string
		body string
		read func(context.Context, *http.Client, string, string, map[string]string) error
	}{
		{"circuit-breaker", `{"attempts":12}`, func(ctx context.Context, client *http.Client, url, name string, keys map[string]string) error {
			count, err := main.ReadTestCircuitCounter(ctx, client, url, &main.TestCircuitBreakerSpec{CounterPath: "/observe", As: name}, keys)
			if err == nil && count != 12 {
				return fmt.Errorf("unexpected attempt count %d", count)
			}
			return err
		}},
		{"connections", `{"instance_id":"` + connectionTestInstance + `","active_connections":2}`, func(ctx context.Context, client *http.Client, url, name string, keys map[string]string) error {
			count, _, err := main.ReadTestConnections(ctx, client, url, &main.TestConnectionLeaksSpec{Path: "/observe", As: name}, keys)
			if err == nil && count != 2 {
				return fmt.Errorf("unexpected connection count %d", count)
			}
			return err
		}},
		{"pool", `{"instance_id":"` + connectionTestInstance + `","capacity":2,"active":0,"queued":0,"peak_active":2,"peak_queued":2,"waiters_created":20,"waiters_finished":20,"waiters_cancelled":0,"waiters_timed_out":0,"max_wait_ms":200,"completed":100}`, func(ctx context.Context, client *http.Client, url, name string, keys map[string]string) error {
			_, err := main.ReadTestPoolSnapshot(ctx, client, url, &main.TestPoolExhaustionSpec{Path: "/observe", As: name}, keys)
			return err
		}},
	}
	for _, reader := range readers {
		for _, name := range []string{"observer", "test-observer"} {
			t.Run(reader.name+"/"+name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Header.Get("Authorization") != "Bearer disposable-test-key" {
						t.Error("observation request omitted the provisioned consumer key")
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = w.Write([]byte(reader.body))
				}))
				defer server.Close()
				envName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
				keys := main.TestHTTPConsumerKeys([]string{"GREGALE_TEST_CONSUMER_" + envName + "_KEY=disposable-test-key"})
				if err := reader.read(t.Context(), server.Client(), server.URL, name, keys); err != nil {
					t.Fatalf("read authenticated observation: %v", err)
				}
				if err := reader.read(t.Context(), server.Client(), server.URL, "missing-consumer", keys); err == nil || !strings.Contains(err.Error(), "credential unavailable") {
					t.Fatalf("missing consumer was not rejected: %v", err)
				}
				if calls.Load() != 1 {
					t.Fatalf("expected one authenticated request, got %d", calls.Load())
				}
			})
		}
	}
}
