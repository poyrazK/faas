package mcphosting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClientPreservesUpstreamHTTPError(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		for _, body := range []string{"", "<html>untrusted-secret-marker</html>", `{malformed`, `{"status":400,"code":"invalid_request","detail":"untrusted-secret-marker"}`} {
			t.Run(fmt.Sprintf("%d/%s", status, body), func(t *testing.T) {
				var requests atomic.Int32
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("discovery forwarded an operator credential")
					}
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, body)
				})
				x, err := c.request(context.Background(), "tools/list", nil, nil, false)
				var upstream *api.APIError
				if !errors.As(err, &upstream) || x.HTTPStatus != status || upstream.Problem.Status != status {
					t.Fatalf("wire=%d error=%v; want typed HTTP %d", x.HTTPStatus, err, status)
				}
				if requests.Load() != 1 || strings.Contains(err.Error(), "untrusted-secret-marker") {
					t.Fatalf("requests=%d error=%v", requests.Load(), err)
				}
				p := upstream.Problem
				if headers := p.HasHeader("Retry-After"); p.RetryAfterSeconds == nil || *p.RetryAfterSeconds != 7 || len(headers) != 1 || headers[0] != "7" {
					t.Fatalf("retry metadata lost: %+v", p)
				}
			})
		}
	}
}

func TestClientRetryAfterValidation(t *testing.T) {
	for _, retry := range []string{"Wed, 21 Oct 2037 07:28:00 GMT", "-1", "arbitrary-header-value", strings.Repeat("x", 4096)} {
		t.Run(retry[:min(len(retry), 35)], func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", retry)
				w.WriteHeader(503)
			})
			_, _, err := c.Tools(context.Background())
			var upstream *api.APIError
			if !errors.As(err, &upstream) {
				t.Fatalf("not a typed HTTP error: %v", err)
			}
			_, dateErr := http.ParseTime(retry)
			if got := upstream.Problem.HasHeader("Retry-After"); (dateErr == nil && (len(got) != 1 || got[0] != retry)) || (dateErr != nil && len(got) != 0) {
				t.Fatalf("retry header=%q, valid HTTP date=%v", got, dateErr == nil)
			}
		})
	}
}

func TestClientHTTPFailureDoesNotRetryToolCall(t *testing.T) {
	var requests atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
	})
	_, err := c.Call(context.Background(), Tool{Name: "charge", InputSchema: map[string]any{}}, nil, false)
	var upstream *api.APIError
	if !errors.As(err, &upstream) || upstream.Problem.Status != 503 || requests.Load() != 1 {
		t.Fatalf("tool call requests=%d error=%v", requests.Load(), err)
	}
}

func TestClientCancellationRetainsCause(t *testing.T) {
	started := make(chan struct{})
	c := testClient(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := c.Tools(ctx); done <- err }()
	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach the server")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation cause lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not cancel")
	}
}

// production-us hunt #4: a revision 2026-07-28 server answers an unsupported
// method with HTTP 404 and a JSON-RPC -32601 body. The client reports the
// JSON-RPC code (never the server's text), not a bare "HTTP 404".
func TestClientReportsJSONRPCErrorInNon2xx(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"untrusted-secret-marker"}}`)
	})
	x, err := c.request(context.Background(), "resources/list", nil, nil, false)
	if !IsMethodNotFound(err) || x.HTTPStatus != http.StatusNotFound || strings.Contains(err.Error(), "untrusted-secret-marker") {
		t.Fatalf("wire=%d err=%v; want a method-not-found RPCError without server text", x.HTTPStatus, err)
	}
}
