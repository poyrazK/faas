package vmmdgrpc

// adr: 20261025150000 — vmmd stops sending per-request invoke events to an
// instance whose guest has no extension endpoint, then probes again.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/extension"
)

func TestInvokeHookBackoff(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	unavailable := fmt.Errorf("vmm: guest has no extension endpoint: %w", extension.ErrUnavailable)
	cases := []struct {
		name      string
		recordErr error
		checkAt   time.Duration
		wantAllow bool
	}{
		{name: "never recorded", checkAt: 0, wantAllow: true},
		{name: "delivered", recordErr: nil, checkAt: time.Second, wantAllow: true},
		{name: "no extension, inside the backoff", recordErr: unavailable, checkAt: time.Second, wantAllow: false},
		{name: "no extension, backoff ended", recordErr: unavailable, checkAt: extension.InvokeUnavailableBackoff, wantAllow: true},
		{name: "transport failure keeps sending", recordErr: errors.New("vmm: extension hook dial vsock uds: refused"), checkAt: time.Second, wantAllow: true},
		{name: "broken extension keeps sending", recordErr: errors.New("vmm: extension hook rejected (ack=10)"), checkAt: time.Second, wantAllow: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b invokeHookBackoff
			if tc.name != "never recorded" {
				b.record("i-1", tc.recordErr, t0)
			}
			if got := b.allow("i-1", t0.Add(tc.checkAt)); got != tc.wantAllow {
				t.Fatalf("allow = %v, want %v", got, tc.wantAllow)
			}
			if !b.allow("i-2", t0.Add(tc.checkAt)) {
				t.Fatal("another instance was held back")
			}
		})
	}
}

func TestInvokeHookBackoffProbesOnceAfterExpiry(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	var b invokeHookBackoff
	b.record("i-1", extension.ErrUnavailable, t0)
	after := t0.Add(extension.InvokeUnavailableBackoff)
	if !b.allow("i-1", after) {
		t.Fatal("first event after the backoff was held back")
	}
	if len(b.until) != 0 {
		t.Fatalf("expired entry kept: %v", b.until)
	}
}

func TestInvokeHookBackoffSweepsExpiredEntries(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	var b invokeHookBackoff
	for i := 0; i < invokeHookBackoffSweepAt; i++ {
		b.record(fmt.Sprintf("old-%d", i), extension.ErrUnavailable, t0)
	}
	b.record("new", extension.ErrUnavailable, t0.Add(extension.InvokeUnavailableBackoff))
	if len(b.until) != 1 {
		t.Fatalf("entries after sweep = %d, want 1", len(b.until))
	}
	if b.allow("new", t0.Add(extension.InvokeUnavailableBackoff+time.Second)) {
		t.Fatal("the instance recorded during the sweep lost its backoff")
	}
}
