package liveproxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
)

// adr: 1384 — a session reaches only what its router routes to: without a
// router nothing, and a renewal sealed with a key of another database is
// refused even though the endpoint may route that key.
func TestSessionRoutesOnlyThroughItsRouter(t *testing.T) {
	p := New(nil, nil)
	defer p.Close()
	router := StaticRouter(map[string]Route{
		"0123456789abcdef": {Database: "db_1", URL: "ws://127.0.0.1:1/live", SealedRole: "reader"}, // unreachable: subscriptions stay
		"fedcba9876543210": {Database: "db_2", URL: "ws://127.0.0.1:1/live", SealedRole: "reader"},
		"1111111111111111": {Database: "db_3", URL: "ws://127.0.0.1:1/live"}, // no binding login
	})
	errorFor := func(frames chan []byte, subscription string) liveproto.Error {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case data := <-frames:
				var e liveproto.Error
				if json.Unmarshal(data, &e) == nil && e.Subscription == subscription && e.Code != liveproto.CodeUnavailable {
					return e
				}
			case <-deadline:
				t.Fatalf("no error for %s", subscription)
			}
		}
	}
	frame := func(kind, subscription, kid string) []byte {
		data, _ := json.Marshal(liveproto.ClientFrame{Type: kind, Subscription: subscription, Sealed: "gls1." + kid + ".x"})
		return data
	}
	ctx := context.Background()

	frames := make(chan []byte, 64)
	s := p.Attach(router, func(f []byte) error { frames <- f; return nil })
	defer s.Close()
	s.Receive(ctx, frame(liveproto.TypeSubscribe, "feed", "0123456789abcdef"))
	s.Receive(ctx, frame(liveproto.TypeRenew, "feed", "fedcba9876543210"))
	if e := errorFor(frames, "feed"); e.Code != liveproto.CodeForbidden || e.Closed {
		t.Fatalf("cross-database renewal: %+v", e)
	}
	s.Receive(ctx, frame(liveproto.TypeSubscribe, "unrouted", "aaaaaaaaaaaaaaaa"))
	if e := errorFor(frames, "unrouted"); e.Code != liveproto.CodeForbidden || !e.Closed {
		t.Fatalf("unrouted key: %+v", e)
	}
	s.Receive(ctx, frame(liveproto.TypeSubscribe, "nologin", "1111111111111111"))
	if e := errorFor(frames, "nologin"); e.Code != liveproto.CodeForbidden || !e.Closed {
		t.Fatalf("route without a login: %+v", e)
	}

	bare := make(chan []byte, 8)
	b := p.Attach(nil, func(f []byte) error { bare <- f; return nil })
	defer b.Close()
	b.Receive(ctx, frame(liveproto.TypeSubscribe, "feed", "0123456789abcdef"))
	if e := errorFor(bare, "feed"); e.Code != liveproto.CodeForbidden || !e.Closed {
		t.Fatalf("session without routes: %+v", e)
	}
}
