// adr: 375
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func TestSyntheticForwardAbortDiscardsPartialResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  error
	}{
		{"transport_abort", nil, http.ErrAbortHandler},
		{"security_revoked", trafficrevocation.ErrRevoked, trafficrevocation.ErrRevoked},
		{"deadline_exceeded", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, app, _, target := invocationDeliveryFixture(t)
			registry := trafficrevocation.New(&syntheticSecurityStore{})
			defer registry.Close()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			adapter := &synthAdapter{store: store, trafficRevocations: registry,
				forward: func(gateway.Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte("partial"))
						if tc.cause != nil {
							cancel(tc.cause)
						}
						panic(http.ErrAbortHandler)
					})
				},
			}
			inv := state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke, Result: json.RawMessage(`"prior"`)}
			out, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, inv, target)
			if !errors.Is(err, tc.want) || status != 0 || len(out.Result) != 0 {
				t.Fatalf("aborted invocation: err=%v status=%d result=%s", err, status, out.Result)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}

func TestSyntheticForwardAbortDoesNotConsumeUnexpectedPanic(t *testing.T) {
	// Panic payloads may be errors or arbitrary values. Recovery must preserve
	// the exact payload, including its identity, for either kind.
	for _, tc := range []struct {
		name    string
		payload any
	}{
		{"error", errors.New("unexpected forwarder panic")},
		{"opaque_value", struct{ Code int }{Code: 17}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, app, _, target := invocationDeliveryFixture(t)
			registry := trafficrevocation.New(&syntheticSecurityStore{})
			defer registry.Close()
			adapter := &synthAdapter{store: store, trafficRevocations: registry,
				forward: func(gateway.Target) http.Handler {
					return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(tc.payload) })
				},
			}
			var caught any
			func() {
				defer func() { caught = recover() }()
				_, _, _ = adapter.InvokeWithTargetStatus(t.Context(), app.ID,
					state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke}, target)
			}()
			if caught != tc.payload {
				t.Fatalf("panic = %v, want exact payload %v", caught, tc.payload)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}

func TestSyntheticInvokeCancellationDiscardsPriorResult(t *testing.T) {
	store, app, _, _ := invocationDeliveryFixture(t)
	registry := trafficrevocation.New(&syntheticSecurityStore{})
	defer registry.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	adapter := &synthAdapter{store: store, trafficRevocations: registry,
		invokeWithStatus: func(_ context.Context, _ string, inv state.Invocation, _ state.InvocationVersion) (state.Invocation, int, error) {
			inv.Result = json.RawMessage(`"partial"`)
			cancel(trafficrevocation.ErrRevoked)
			return inv, http.StatusOK, nil
		},
	}
	out, status, err := adapter.InvokeWithStatus(ctx, app.ID,
		state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke, Result: json.RawMessage(`"prior"`)})
	if !errors.Is(err, trafficrevocation.ErrRevoked) || status != 0 || len(out.Result) != 0 {
		t.Fatalf("revoked invocation: err=%v status=%d result=%s", err, status, out.Result)
	}
	assertSyntheticSecurityReleased(t, registry)
}
