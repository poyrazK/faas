package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// adr: 500
func TestConsumption429SuppressesSubsequentRequests(t *testing.T) {
	var consumptionCalls, lifecycleCalls atomic.Int32
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/projects" {
			lifecycleCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		consumptionCalls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	request := func(path string) error {
		return provider.doJSON(t.Context(), http.MethodGet, path, nil, nil, nil, http.StatusNoContent)
	}
	if err := request("/consumption_history/v2/projects"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatal(err)
	}
	var requests sync.WaitGroup
	for _, path := range []string{"/consumption_history/v2/projects", "/consumption_history/v2/branches", "/consumption_history/projects"} {
		for range 8 {
			requests.Go(func() {
				if err := request(path); !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Errorf("cooldown error = %v", err)
				}
			})
		}
	}
	requests.Wait()
	if err := request("/projects"); err != nil {
		t.Fatal(err)
	}
	if consumptionCalls.Load() != 1 || lifecycleCalls.Load() != 1 {
		t.Fatalf("consumption quota still hit or lifecycle blocked: consumption=%d lifecycle=%d", consumptionCalls.Load(), lifecycleCalls.Load())
	}
}

type clientTestTransport func(*http.Request) (*http.Response, error)

func (f clientTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderJSONPinsProductionOriginAndRefusesRedirects(t *testing.T) {
	provider, err := New(testBackend(), func(string) string { return "test-only" })
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*Provider)
	var calls int
	p.httpClient.Transport = clientTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "console.neon.tech" || r.URL.User != nil {
			t.Fatalf("caller changed production authority: %s", r.URL.Redacted())
		}
		status := http.StatusNoContent
		header := make(http.Header)
		if r.URL.Path == "/api/v2/redirect" {
			status = http.StatusFound
			header.Set("Location", "http://127.0.0.1/private")
		}
		return &http.Response{StatusCode: status, Header: header, Body: http.NoBody, Request: r}, nil
	})
	for _, path := range []string{"//127.0.0.1/private", "/https://127.0.0.1/private", "/projects/../private"} {
		if err := p.doJSON(t.Context(), http.MethodGet, path, url.Values{"host": {"127.0.0.1"}}, nil, nil, http.StatusNoContent); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.doJSON(t.Context(), http.MethodGet, "/redirect", nil, nil, nil, http.StatusNoContent); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("redirect accepted: %v", err)
	}
	if calls != 4 {
		t.Fatalf("redirect caused another request: %d calls", calls)
	}
}

type clientTestBody struct{ read func([]byte) (int, error) }

func (b clientTestBody) Read(p []byte) (int, error) { return b.read(p) }
func (clientTestBody) Close() error                 { return nil }

func clientTransportProvider(transport http.RoundTripper) *Provider {
	baseURL, _ := url.Parse("https://provider.example/api/v2")
	return newProvider("eu-central-1", "org-test", "secret", baseURL, &http.Client{Transport: transport}, settings{})
}

func TestProviderJSONPreservesBodyReadCancellation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			provider := clientTransportProvider(clientTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: clientTestBody{read: func([]byte) (int, error) {
					cancel()
					return 0, ctx.Err()
				}}, Request: r}, nil
			}))
			var output map[string]any
			if err := provider.doJSON(ctx, http.MethodGet, "/projects", nil, nil, &output, http.StatusOK); !errors.Is(err, context.Canceled) {
				t.Fatalf("response-body cancellation = %v, want context.Canceled", err)
			}
		})
	}
}

func TestProviderJSONHonorsCanceledContextBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	provider := clientTransportProvider(clientTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := provider.doJSON(ctx, http.MethodGet, "/projects", nil, nil, nil, http.StatusNoContent); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled context reached transport %d times", calls.Load())
	}
}
