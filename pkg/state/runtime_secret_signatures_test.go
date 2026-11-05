package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261028161502 — runtime secret signatures are kept by envelope
// digest, and a write replaces a different signature for the same digest.
func TestRuntimeSecretSignatures(t *testing.T) {
	t.Run("mem", func(t *testing.T) {
		checkRuntimeSecretSignatures(t, context.Background(), state.NewMemStore())
	})
	t.Run("pg", func(t *testing.T) {
		s, ctx := pgStore(t)
		checkRuntimeSecretSignatures(t, ctx, s)
	})
}

func checkRuntimeSecretSignatures(t *testing.T, ctx context.Context, store state.RuntimeSecretSignatureStore) {
	t.Helper()
	first, second, absent := sha256.Sum256([]byte("envelope-1")), sha256.Sum256([]byte("envelope-2")), sha256.Sum256([]byte("absent"))
	sigA, sigB := bytes.Repeat([]byte{0xa}, 64), bytes.Repeat([]byte{0xb}, 64)
	for _, put := range []struct {
		digest, sig []byte
	}{{first[:], sigA}, {second[:], sigA}, {first[:], sigA}, {second[:], sigB}} {
		if err := store.PutRuntimeSecretSignature(ctx, put.digest, put.sig); err != nil {
			t.Fatalf("PutRuntimeSecretSignature: %v", err)
		}
	}
	got, err := store.RuntimeSecretSignatures(ctx, [][]byte{first[:], second[:], absent[:]})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !bytes.Equal(got[string(first[:])], sigA) || !bytes.Equal(got[string(second[:])], sigB) {
		t.Fatalf("RuntimeSecretSignatures = %x", got)
	}
	if err := store.PutRuntimeSecretSignature(ctx, first[:4], sigA); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a short digest = %v", err)
	}
	if err := store.PutRuntimeSecretSignature(ctx, first[:], sigA[:10]); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a short signature = %v", err)
	}
	if _, err := store.RuntimeSecretSignatures(ctx, make([][]byte, state.MaxRuntimeSecretSignatureLookup+1)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("an oversized lookup = %v", err)
	}
}
