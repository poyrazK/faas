package sealsig_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/sealsig"
)

// memSignatures is a RuntimeStore that counts lookups and can fail them.
type memSignatures struct {
	sigs    map[string][]byte
	lookups int
	fail    error
}

func (m *memSignatures) PutRuntimeSecretSignature(_ context.Context, digest, sig []byte) error {
	if m.sigs == nil {
		m.sigs = map[string][]byte{}
	}
	m.sigs[string(digest)] = sig
	return nil
}

func (m *memSignatures) RuntimeSecretSignatures(_ context.Context, digests [][]byte) (map[string][]byte, error) {
	m.lookups++
	if m.fail != nil {
		return nil, m.fail
	}
	out := map[string][]byte{}
	for _, d := range digests {
		if sig, ok := m.sigs[string(d)]; ok {
			out[string(d)] = sig
		}
	}
	return out, nil
}

func newKey(t *testing.T) (ed25519.PublicKey, *sealsig.Signer) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, sealsig.NewSigner(private)
}

func TestRuntimeSealHookStoresTheSignature(t *testing.T) {
	public, signer := newKey(t)
	store := &memSignatures{}
	blob := []byte("age-envelope")
	if err := sealsig.RuntimeSealHook(context.Background(), store, signer)(blob); err != nil {
		t.Fatal(err)
	}
	sig := store.sigs[string(sealsig.RuntimeDigest(blob))]
	if !ed25519.Verify(public, sealsig.RuntimeSecretMessage(blob), sig) {
		t.Fatal("the stored signature does not verify for the envelope")
	}
}

func TestRuntimeCheckerVerifiesBeforeOpening(t *testing.T) {
	public, signer := newKey(t)
	_, forger := newKey(t)
	signed, forged, unsigned := []byte("signed"), []byte("forged"), []byte("unsigned")
	store := &memSignatures{}
	ctx := context.Background()
	if err := sealsig.SignRuntime(ctx, store, signer, signed); err != nil {
		t.Fatal(err)
	}
	if err := sealsig.SignRuntime(ctx, store, forger, forged); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		require bool
		blobs   [][]byte
		refused int // index of the refused blob, -1 for none
		want    error
	}{
		{"signed", true, [][]byte{signed}, -1, nil},
		{"forged", false, [][]byte{signed, forged}, 1, sealsig.ErrBadSignature},
		{"unsigned before the contract step", false, [][]byte{unsigned, signed}, -1, nil},
		{"unsigned after the contract step", true, [][]byte{signed, unsigned}, 1, sealsig.ErrUnsigned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker, err := sealsig.NewRuntimeChecker(store, sealsig.NewVerifier([]ed25519.PublicKey{public}, tc.require), slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			err = checker.Check(ctx, tc.blobs)
			if tc.refused < 0 {
				if err != nil {
					t.Fatalf("Check = %v", err)
				}
				return
			}
			var refused *sealsig.BlobError
			if !errors.As(err, &refused) || refused.Index != tc.refused || !errors.Is(err, tc.want) {
				t.Fatalf("Check = %v, want %v at %d", err, tc.want, tc.refused)
			}
		})
	}
}

func TestRuntimeCheckerRemembersVerifiedEnvelopes(t *testing.T) {
	public, signer := newKey(t)
	store := &memSignatures{}
	blob := []byte("envelope")
	ctx := context.Background()
	if err := sealsig.SignRuntime(ctx, store, signer, blob); err != nil {
		t.Fatal(err)
	}
	checker, err := sealsig.NewRuntimeChecker(store, sealsig.NewVerifier([]ed25519.PublicKey{public}, true), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := checker.Check(ctx, [][]byte{blob, bytes.Clone(blob)}); err != nil {
			t.Fatal(err)
		}
	}
	if store.lookups != 1 {
		t.Fatalf("lookups = %d, want 1: a verified envelope is not looked up again", store.lookups)
	}
}

func TestRuntimeCheckerLookupFailure(t *testing.T) {
	public, _ := newKey(t)
	store := &memSignatures{fail: errors.New("database down")}
	ctx := context.Background()
	expand, _ := sealsig.NewRuntimeChecker(store, sealsig.NewVerifier([]ed25519.PublicKey{public}, false), slog.Default())
	if err := expand.Check(ctx, [][]byte{[]byte("x")}); err != nil {
		t.Fatalf("before the contract step a failed lookup counts as unsigned: %v", err)
	}
	contract, _ := sealsig.NewRuntimeChecker(store, sealsig.NewVerifier([]ed25519.PublicKey{public}, true), slog.Default())
	if err := contract.Check(ctx, [][]byte{[]byte("x")}); err == nil {
		t.Fatal("after the contract step a failed lookup must fail the check")
	}
	var none *sealsig.RuntimeChecker
	if err := none.Check(ctx, [][]byte{[]byte("x")}); err != nil {
		t.Fatalf("a nil checker = %v", err)
	}
}
