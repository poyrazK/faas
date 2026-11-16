package basicverifier_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/basicverifier"
)

// adr: 20261110210000 slice 3h — a verifier checks a credential without
// holding it.
func TestVerifierRoundTrip(t *testing.T) {
	ctx := context.Background()
	v, err := basicverifier.New(ctx, "alice", "s3cret pass")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(v, "s3cret") {
		t.Fatalf("verifier = %q", v)
	}
	for _, tc := range []struct {
		user, pass string
		want       bool
	}{
		{"alice", "s3cret pass", true},
		{"alice", "s3cret pas", false},
		{"bob", "s3cret pass", false},
		// The newline layout keeps user and pass apart.
		{"alice\ns3cret", "pass", false},
	} {
		ok, err := basicverifier.Verify(ctx, v, tc.user, tc.pass)
		if err != nil || ok != tc.want {
			t.Errorf("Verify(%q, %q) = %v, %v; want %v", tc.user, tc.pass, ok, err, tc.want)
		}
	}
	other, _ := basicverifier.New(ctx, "alice", "s3cret pass")
	if other == v {
		t.Fatal("two verifiers of one credential share a salt")
	}
}

func TestVerifierRefusesForeignOrCostlyStrings(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA", // 4 GiB
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$!!$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		if _, err := basicverifier.Verify(ctx, bad, "u", "p"); !errors.Is(err, basicverifier.ErrMalformed) {
			t.Errorf("Verify(%q) = %v, want ErrMalformed", bad, err)
		}
	}
}

func TestKeyIsTheSealedBlobDigest(t *testing.T) {
	a, b := basicverifier.Key([]byte("blob-1")), basicverifier.Key([]byte("blob-2"))
	if len(a) != 32 || string(a) == string(b) {
		t.Fatalf("keys = %x, %x", a, b)
	}
}
