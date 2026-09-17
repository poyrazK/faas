package state

import (
	"context"
	"testing"
)

func TestPGRateLimitBackend_ConsumeTokenRejectsInvalidPolicy(t *testing.T) {
	b := &PGRateLimitBackend{}
	for _, tc := range []struct {
		name       string
		rps, burst float64
	}{
		{name: "zero rps", rps: 0, burst: 1},
		{name: "negative rps", rps: -1, burst: 1},
		{name: "zero burst", rps: 1, burst: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remaining, ok, err := b.ConsumeToken(context.Background(), "app", "subject", "hobby", tc.rps, tc.burst)
			if err != nil || ok || remaining != 0 {
				t.Fatalf("ConsumeToken() = (%d,%v,%v), want (0,false,nil)", remaining, ok, err)
			}
		})
	}
}

func TestPGRateLimitBackend_InvalidateNoOp(t *testing.T) {
	b := &PGRateLimitBackend{}
	b.Invalidate("app", "subject", "hobby")
	b.Invalidate("", "", "")
}

func TestPGRateLimitBackend_NewStoresFields(t *testing.T) {
	b := NewPGRateLimitBackend(nil)
	if b == nil || b.pool != nil {
		t.Fatalf("backend=%+v", b)
	}
}
