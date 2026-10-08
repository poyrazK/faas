package trafficdeadline

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeadlineCannotResetAcrossIndependentSigners(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{42}, 32)
	first, err := New(key, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(append([]byte(nil), key...), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Second)
	chain := uuid.NewString()
	for _, app := range []string{"app-a", "app-b", "app-c"} {
		token, mintErr := first.Mint(app, "account", chain, deadline)
		if mintErr != nil {
			t.Fatal(mintErr)
		}
		now = now.Add(200 * time.Millisecond)
		claims, verifyErr := second.Verify(token, app)
		if verifyErr != nil || claims.ChainID != chain || !claims.Deadline().Equal(deadline) {
			t.Fatalf("claims=%+v error=%v; deadline changed across hop", claims, verifyErr)
		}
	}
	now = deadline
	token, err := first.Mint("app-c", "account", chain, deadline)
	if !errors.Is(err, ErrExpired) || token != "" {
		t.Fatalf("expired mint returned %q error=%v", token, err)
	}
}

func TestDeadlineTokenRefusesUntrustedAndUnavailableClaims(t *testing.T) {
	now := time.Now()
	key := bytes.Repeat([]byte{42}, 32)
	signer, err := New(key, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint("app-a", "account-a", uuid.NewString(), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	tampered := append([]string(nil), parts...)
	tampered[2] = base64.RawURLEncoding.EncodeToString([]byte(`{"a":"app-a","d":999999999999999999}`))
	wrongKey, err := New(bytes.Repeat([]byte{24}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, app string
		signer           *Signer
		want             error
	}{
		{"tamper", strings.Join(tampered, "."), "app-a", signer, ErrInvalid},
		{"wrong-app", token, "app-b", signer, ErrInvalid},
		{"duplicate", token + "," + token, "app-a", signer, ErrInvalid},
		{"oversized", strings.Repeat("x", api.MaxTrafficDeadlineTokenBytes+1), "app-a", signer, ErrInvalid},
		{"version", "v2" + token[2:], "app-a", signer, ErrInvalid},
		{"wrong-key", token, "app-a", wrongKey, ErrUnavailable},
		{"missing-key", token, "app-a", nil, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, verifyErr := tc.signer.Verify(tc.token, tc.app); !errors.Is(verifyErr, tc.want) {
				t.Fatalf("error=%v, want %v", verifyErr, tc.want)
			}
		})
	}
	now = now.Add(-time.Millisecond)
	if _, err := signer.Verify(token, "app-a"); !errors.Is(err, ErrClock) {
		t.Fatalf("future issue error=%v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := signer.Verify(token, "app-a"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token error=%v", err)
	}
}

func TestDeadlineTokenEnforcesLifetimeAndIdentityBounds(t *testing.T) {
	now := time.Now()
	signer, err := New(bytes.Repeat([]byte{42}, 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		app, account, chain string
		deadline            time.Time
	}{
		{"", "account", uuid.NewString(), now.Add(time.Second)},
		{"app", "", uuid.NewString(), now.Add(time.Second)},
		{"app", "account", uuid.Nil.String(), now.Add(time.Second)},
		{"app", "account", "untrusted-chain", now.Add(time.Second)},
		{"app", "account", uuid.NewString(), now.Add(time.Duration(api.MaxServiceReliabilityTimeoutMS+1) * time.Millisecond)},
	} {
		if _, err := signer.Mint(tc.app, tc.account, tc.chain, tc.deadline); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad claim error=%v", err)
		}
	}
	if _, err := New(bytes.Repeat([]byte{42}, 31), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("short key error=%v", err)
	}
}
