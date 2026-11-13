package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 slice 3h — a verifier is keyed by its sealed blob and
// never replaced; Pg and Mem agree.
func TestBasicAuthVerifierStore(t *testing.T) {
	pg, ctx := pgStore(t)
	for name, store := range map[string]state.BasicAuthVerifierStore{"pg": pg, "mem": state.NewMemStore()} {
		t.Run(name, func(t *testing.T) {
			checkBasicAuthVerifierStore(ctx, t, store)
		})
	}
}

func checkBasicAuthVerifierStore(ctx context.Context, t *testing.T, store state.BasicAuthVerifierStore) {
	t.Helper()
	sum := sha256.Sum256([]byte("sealed-" + uuid.NewString()))
	key := sum[:]
	if _, err := store.AppBasicAuthVerifier(ctx, key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("absent verifier = %v, want ErrNotFound", err)
	}
	if err := store.PutAppBasicAuthVerifier(ctx, key, "$argon2id$first"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAppBasicAuthVerifier(ctx, key, "$argon2id$second"); err != nil {
		t.Fatal(err)
	}
	if v, err := store.AppBasicAuthVerifier(ctx, key); err != nil || v != "$argon2id$first" {
		t.Fatalf("verifier = %q, %v; want the first", v, err)
	}
	if err := store.PutAppBasicAuthVerifier(ctx, []byte("short"), "$argon2id$x"); err == nil {
		t.Fatal("a key that is not a digest was accepted")
	}
}
