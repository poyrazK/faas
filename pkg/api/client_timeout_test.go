package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRequestTimeoutDefaultsAndIsolation(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, 90 * time.Second} {
		client := api.NewClientWithRequestTimeout("https://example.test", "", duration)
		want := duration
		if want <= 0 {
			want = 30 * time.Second
		}
		if got := client.HTTPClient().Timeout; got != want {
			t.Errorf("timeout %s: got %s, want %s", duration, got, want)
		}
	}
	if api.NewClient("https://example.test", "").HTTPClient().Timeout != 30*time.Second {
		t.Fatal("explicit client changed another client's defaults")
	}
}

// A flushed response distinguishes a whole-request deadline from a header wait.
func TestRequestTimeoutIncludesResponseBodyAndCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	for _, upload := range []bool{false, true} {
		for _, callerDeadline := range []bool{false, true} {
			t.Run(strings.Join([]string{boolName(upload), boolName(callerDeadline)}, "/"), func(t *testing.T) {
				budget := 75 * time.Millisecond
				if callerDeadline {
					budget = time.Second
				}
				client := api.NewClientWithRequestTimeout(server.URL, "", budget)
				ctx := t.Context()
				if callerDeadline {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 75*time.Millisecond)
					defer cancel()
				}
				var err error
				if upload {
					_, err = client.DeployMultipart(ctx, "app", strings.NewReader("source"), "source.tgz", "node22", "index.handler", false, api.DeployAnnotations{})
				} else {
					_, err = client.UsageSummary(ctx, "")
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected response-body deadline, got %v", err)
				}
			})
		}
	}
}

func boolName(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

type timeoutRoundTripFunc func(*http.Request) (*http.Response, error)

func (f timeoutRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Explicit budgets must survive operation-specific clients and transport wrappers.
func TestRequestTimeoutOperationBudgets(t *testing.T) {
	operations := map[string]func(*api.Client) error{
		"rollback": func(c *api.Client) error { _, err := c.Rollback(t.Context(), "app"); return err },
		"hosting finalize": func(c *api.Client) error {
			_, err := c.FinalizeHostingRelease(t.Context(), "site", "release")
			return err
		},
		"invoice history": func(c *api.Client) error { _, err := c.BackfillInvoiceHistory(t.Context(), "", 0); return err },
		"invoice refresh": func(c *api.Client) error { _, err := c.RefreshInvoiceFacts(t.Context(), "invoice"); return err },
		"webhook replay": func(c *api.Client) error {
			_, err := c.ReplayDevBridgeWebhook(t.Context(), "bridge", api.ReplayDevBridgeWebhookRequest{})
			return err
		},
		"upload": func(c *api.Client) error {
			_, err := c.DeployMultipart(t.Context(), "app", strings.NewReader("source"), "source.tgz", "node22", "index.handler", false, api.DeployAnnotations{})
			return err
		},
	}
	for name, operation := range operations {
		for _, explicit := range []bool{false, true} {
			t.Run(name+"/explicit="+boolName(explicit), func(t *testing.T) {
				client := api.NewClient("https://example.test", "private-token")
				if explicit {
					client = api.NewClientWithRequestTimeout("https://example.test", "private-token", time.Second)
				}
				calls := 0
				client.WrapTransport(func(http.RoundTripper) http.RoundTripper {
					return timeoutRoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						deadline, ok := r.Context().Deadline()
						remaining := time.Until(deadline)
						if !ok || remaining <= 0 || explicit && remaining > time.Second || !explicit && remaining < 20*time.Second {
							t.Errorf("unexpected request budget: deadline=%t remaining=%s", ok, remaining)
						}
						if r.Header.Get("Authorization") != "Bearer private-token" {
							t.Error("authentication lost")
						}
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: 2, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
					})
				})
				if err := operation(client); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("wrapper saw %d requests, want one", calls)
				}
			})
		}
	}
}

func TestRequestTimeoutStreamHeaderWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := api.NewClientWithRequestTimeout(server.URL, "", 75*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	body, err := client.StreamDeploymentLogs(ctx, "deployment", nil, 10, true)
	if body != nil {
		_ = body.Close()
	}
	var timeout interface{ Timeout() bool }
	if !errors.As(err, &timeout) || !timeout.Timeout() || ctx.Err() != nil {
		t.Fatalf("expected response-header timeout, got %v", err)
	}
}

func TestRequestTimeoutStreamBodyRemainsUnbounded(t *testing.T) {
	// An inherited smaller header cap must be replaced, without mutating the
	// process default or applying the total deadline to the stream body.
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Nanosecond
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		_, _ = io.WriteString(w, "data: still connected\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := api.NewClientWithRequestTimeout(server.URL, "", 150*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	body, err := client.StreamDeploymentLogs(ctx, "deployment", nil, 10, true)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	want := "data: still connected\n\n"
	data := make([]byte, len(want))
	if _, err := io.ReadFull(body, data); err != nil || string(data) != want {
		t.Fatalf("stream body ended at request timeout: %q, %v", data, err)
	}
	cancel()
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller could not cancel stream: %v", err)
	}
	if transport.ResponseHeaderTimeout != time.Nanosecond {
		t.Fatal("constructor mutated the default transport")
	}
}
