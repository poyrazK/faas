package neon

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// adr: 500
func TestRateLimitCooldownHonorsRetryAfterAndExpires(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, header string
		delay        time.Duration
	}{
		{"seconds", "120", 2 * time.Minute},
		{"whitespace", " 120 ", 2 * time.Minute},
		{"http date", start.Add(3 * time.Minute).Format(http.TimeFormat), 3 * time.Minute},
		{"missing", "", time.Minute},
		{"zero", "0", time.Minute},
		{"negative", "-1", time.Minute},
		{"signed", "+120", time.Minute},
		{"malformed", "private-provider-error", time.Minute},
		{"expired date", start.Add(-time.Minute).Format(http.TimeFormat), time.Minute},
		{"duration overflow", "9223372037", time.Minute},
		{"integer overflow", "9223372036854775808", time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", tt.header)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			now := start
			provider.now = func() time.Time { return now }
			request := func() error {
				return provider.doJSON(t.Context(), http.MethodGet, "/consumption_history/v2/projects", nil, nil, nil, http.StatusNoContent)
			}
			if err := request(); !errors.Is(err, managedpostgres.ErrUnavailable) {
				t.Fatal(err)
			}
			now = start.Add(tt.delay - time.Nanosecond)
			if err := request(); !errors.Is(err, managedpostgres.ErrUnavailable) || calls.Load() != 1 {
				t.Fatalf("request before deadline: %v, HTTP calls=%d", err, calls.Load())
			}
			now = start.Add(tt.delay)
			if err := request(); err != nil || calls.Load() != 2 {
				t.Fatalf("request at deadline: %v, HTTP calls=%d", err, calls.Load())
			}
		})
	}
}

func TestGeneralRateLimitCooldownAlsoDefersConsumption(t *testing.T) {
	var calls atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	for _, path := range []string{"/projects", "/projects/project-a/roles", "/consumption_history/v2/projects"} {
		if err := provider.doJSON(t.Context(), http.MethodGet, path, nil, nil, nil, http.StatusNoContent); !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("general rate limit ignored: HTTP calls=%d", calls.Load())
	}
}

func TestConcurrentRateLimitResponsesDoNotShortenCooldown(t *testing.T) {
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	now := start
	provider.now = func() time.Time { return now }
	var responses sync.WaitGroup
	for _, header := range []string{"300", "60", "120", "0", "bad"} {
		responses.Go(func() { provider.deferRequests("/consumption_history/v2/projects", header) })
	}
	responses.Wait()
	// A shorter response arriving after the longest response must not reset it.
	provider.deferRequests("/consumption_history/v2/projects", "60")
	now = start.Add(2 * time.Minute)
	if !provider.requestsDeferred("/consumption_history/v2/projects") || provider.requestsDeferred("/projects") {
		t.Fatal("concurrent responses shortened or leaked consumption cooldown")
	}
	now = start.Add(5 * time.Minute)
	if provider.requestsDeferred("/consumption_history/v2/projects") {
		t.Fatal("cooldown did not expire")
	}
}

func TestResourceLockDoesNotEstablishAccountCooldown(t *testing.T) {
	var calls atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusLocked)
	}))
	for range 2 {
		if err := provider.doJSON(t.Context(), http.MethodGet, "/projects", nil, nil, nil, http.StatusNoContent); !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("a resource lock deferred unrelated requests")
	}
}

func TestProvisionRateLimitDoesNotReplayCreateWithinCall(t *testing.T) {
	var gets, posts atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets.Add(1)
			writeResponse(t, w, http.StatusOK, map[string]any{"projects": []any{}})
			return
		}
		posts.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	request := managedpostgres.ProvisionRequest{ResourceID: "db-rate-limited", IdempotencyKey: "create", Spec: testDatabaseSpec()}
	for range 2 {
		if _, err := provider.Provision(t.Context(), request); !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if gets.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("rate-limited create retried: GET=%d POST=%d", gets.Load(), posts.Load())
	}
}
