package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
)

func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestNewManager_RejectsShortKey(t *testing.T) {
	if _, err := session.NewManager([]byte("short"), time.Hour); err == nil {
		t.Fatal("expected error for short key")
	}
}

// TestNewManager_ZeroesCallerKey confirms the caller's key slice is
// wiped on a successful NewManager. The Manager itself keeps only
// the AEAD; the caller's slice must not retain the secret.
func TestNewManager_ZeroesCallerKey(t *testing.T) {
	k := key(t)
	// Snapshot a non-zero byte — if NewManager didn't wipe, we'd see it.
	original := append([]byte(nil), k...)
	if _, err := session.NewManager(k, time.Hour); err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for i, b := range k {
		if b != 0 {
			t.Errorf("caller key not zeroed at index %d (got 0x%02x; original 0x%02x)", i, b, original[i])
		}
	}
}

func TestIssue_And_Verify_RoundTrip(t *testing.T) {
	m, err := session.NewManager(key(t), time.Hour)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	v, err := m.Issue("acct-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	env, err := m.Verify(v)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if env.AccountID != "acct-1" {
		t.Errorf("account = %q, want acct-1", env.AccountID)
	}
	if env.ExpiresAt.Before(time.Now()) {
		t.Errorf("expires = %v, want in future", env.ExpiresAt)
	}
}

func TestVerify_RejectsTampered(t *testing.T) {
	m, _ := session.NewManager(key(t), time.Hour)
	v, _ := m.Issue("acct-1")
	// Flip a base64 char in the middle of the encoded blob.
	tampered := v[:len(v)/2] + "X" + v[len(v)/2+1:]
	if _, err := m.Verify(tampered); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestVerify_RejectsEmptyEnvelope(t *testing.T) {
	m, _ := session.NewManager(key(t), time.Hour)
	if _, err := m.Verify(""); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("empty cookie err = %v, want ErrInvalid", err)
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m, err := session.NewManager(key(t), time.Hour)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.SetClock(func() time.Time { return now })
	v, err := m.Issue("acct-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Advance past maxAge (1h).
	m.SetClock(func() time.Time { return now.Add(time.Hour + time.Minute) })
	if _, err := m.Verify(v); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("expired err = %v, want ErrInvalid", err)
	}
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	m1, _ := session.NewManager(key(t), time.Hour)
	m2, _ := session.NewManager([]byte(strings.Repeat("x", 32)), time.Hour)
	v, _ := m1.Issue("acct-1")
	if _, err := m2.Verify(v); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("wrong-key err = %v, want ErrInvalid", err)
	}
}

func TestNewEphemeralManager_RoundTrip(t *testing.T) {
	m, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatalf("NewEphemeralManager: %v", err)
	}
	v, err := m.Issue("acct-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	env, err := m.Verify(v)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if env.AccountID != "acct-1" {
		t.Errorf("account = %q", env.AccountID)
	}
}
