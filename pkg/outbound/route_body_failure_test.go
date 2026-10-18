// adr: 991
package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteHandlesProviderBodyFailureBeforeCommit(t *testing.T) {
	unexpectedPanic := errors.New("unexpected provider reader panic")
	for _, tc := range []struct {
		name          string
		status        int
		panicValue    error
		wantAbort     bool
		cancelRequest bool
	}{
		{name: "retryable response", status: http.StatusServiceUnavailable},
		{name: "committed success", status: http.StatusOK, wantAbort: true},
		{name: "committed client error", status: http.StatusBadRequest, wantAbort: true},
		{name: "unexpected panic", status: http.StatusServiceUnavailable, panicValue: unexpectedPanic},
		{name: "canceled request", status: http.StatusServiceUnavailable, wantAbort: true, cancelRequest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRouteFixture(t, tc.status, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transport := fixture.handler.Client.Transport
			fixture.handler.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := transport.RoundTrip(r)
				if err == nil && r.Header.Get("Authorization") == "Bearer primary" {
					broken := &routeBrokenResponseBody{ReadCloser: resp.Body, panicValue: tc.panicValue}
					if tc.cancelRequest {
						broken.cancel = cancel
					}
					resp.Body = broken
				}
				return resp, err
			})
			req := httptest.NewRequest(http.MethodPost, "http://relay/r/route-1/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(withRelayIdentity(ctx, WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
			response := httptest.NewRecorder()
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				fixture.handler.ServeHTTP(response, req)
			}()
			wantPanic := tc.panicValue
			if tc.wantAbort {
				wantPanic = http.ErrAbortHandler
			}
			if recovered != wantPanic { //nolint:errorlint // Assert the exact panic value recognized by net/http.
				t.Errorf("handler panic = %v, want %v", recovered, wantPanic)
			}
			if wantPanic == nil {
				if response.Code != http.StatusOK || response.Header().Get(RouteTargetHeader) != "backup" || len(fixture.calls()) != 2 {
					t.Errorf("failover response = %d, target=%q, calls=%v", response.Code, response.Header().Get(RouteTargetHeader), fixture.calls())
				}
			} else if len(fixture.calls()) != 1 {
				t.Errorf("unexpected fallback after an abort or application panic: %v", fixture.calls())
			}
		})
	}
}

type routeBrokenResponseBody struct {
	io.ReadCloser
	panicValue error
	cancel     context.CancelFunc
}

func (r *routeBrokenResponseBody) Read(p []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
	}
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	if len(p) > 1 {
		p = p[:1]
	}
	n, _ := r.ReadCloser.Read(p)
	return n, io.ErrUnexpectedEOF
}
