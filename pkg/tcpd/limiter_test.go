package tcpd

import "testing"

func TestConnectionLimiterScopesReservationsByAccount(t *testing.T) {
	limiter := NewConnectionLimiter(1)

	releaseA, ok := limiter.Acquire("acct-a")
	if !ok {
		t.Fatal("first reservation for acct-a was rejected")
	}
	if got := limiter.Current("acct-a"); got != 1 {
		t.Fatalf("Current(acct-a) = %d, want 1", got)
	}
	if _, ok := limiter.Acquire("acct-a"); ok {
		t.Fatal("second reservation for acct-a was accepted over the cap")
	}
	if _, ok := limiter.Acquire("acct-b"); !ok {
		t.Fatal("reservation for acct-b was blocked by acct-a")
	}

	releaseA()
	releaseA() // release is deliberately idempotent
	if got := limiter.Current("acct-a"); got != 0 {
		t.Fatalf("Current(acct-a) after release = %d, want 0", got)
	}
}

func TestConnectionLimiterRejectsEmptyKey(t *testing.T) {
	limiter := NewConnectionLimiter(1)
	if _, ok := limiter.Acquire("  "); ok {
		t.Fatal("empty account key was accepted")
	}
}

func TestConnectionLimiterNilIsUnlimited(t *testing.T) {
	var limiter *ConnectionLimiter
	release, ok := limiter.Acquire("")
	if !ok {
		t.Fatal("nil limiter rejected a reservation")
	}
	release()
}
