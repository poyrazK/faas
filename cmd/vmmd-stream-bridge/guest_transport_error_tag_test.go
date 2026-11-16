package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/bridgecompletion"
)

func TestMarkGuestTransportErrorEchoesExchange(t *testing.T) {
	newReq := func(exchange string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/svc/M", nil)
		if exchange != "" {
			r.Header.Set(bridgecompletion.ExchangeHeader, exchange)
		}
		return r
	}
	live := context.Background()
	done, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name     string
		exchange string
		ctx      context.Context
		want     string
	}{
		{"transport failure is tagged", "ex-1", live, "ex-1"},
		{"own deadline is not a transport fault", "ex-1", done, ""},
		{"no exchange, nothing to echo", "", live, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			markGuestTransportError(w, newReq(tc.exchange), tc.ctx)
			if got := w.Header().Get(bridgecompletion.GuestErrorHeader); got != tc.want {
				t.Fatalf("%s = %q, want %q", bridgecompletion.GuestErrorHeader, got, tc.want)
			}
		})
	}
}
