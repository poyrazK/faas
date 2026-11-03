package pgproxy

import (
	"testing"
	"time"
)

// Bug hunt 34 (ADR-1027 "rate-limits per database and per source"): one
// source's failed logins to one database are bounded per minute; another
// source or database is not affected, and the window resets.
func TestAuthFailuresLimitPerDatabaseAndSource(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f := newAuthFailures(3)
	key := authFailureKey("db-1", "198.51.100.7")
	for i := range 3 {
		if f.blocked(key, now) {
			t.Fatalf("blocked after %d failures, limit 3", i)
		}
		f.fail(key, now)
	}
	if !f.blocked(key, now.Add(30*time.Second)) {
		t.Fatal("not blocked after 3 failures within the window")
	}
	if f.blocked(authFailureKey("db-1", "198.51.100.8"), now) || f.blocked(authFailureKey("db-2", "198.51.100.7"), now) {
		t.Fatal("another source or database was blocked")
	}
	if f.blocked(key, now.Add(authFailureWindow)) {
		t.Fatal("still blocked after the window")
	}
	f.fail(key, now.Add(authFailureWindow))
	if f.blocked(key, now.Add(authFailureWindow)) {
		t.Fatal("a new window started at its old count")
	}
}
