package sealsig_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/sealsig"
)

// The message format is a stored contract: signatures written by one release
// verify in the next. This pins the live-query key message the first release
// shipped (ADR 20261028161502).
func TestMessageKnownAnswer(t *testing.T) {
	msg := sealsig.Message("livequery", "live-key", []byte("3f1c0b6e-9a2b-44d1-8c7e-0123456789ab"), []byte("aaaaaaaaaaaaaaaa"), []byte("age1recipient"), []byte("sealed"))
	sum := sha256.Sum256(msg)
	if got, want := hex.EncodeToString(sum[:]), "f822bfa359dc6e592c5cb23caae4dc80301570eb620128befa23d96e7860b449"; got != want {
		t.Fatalf("message digest = %s, want %s", got, want)
	}
}

func TestVerifierChecksSignatures(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := sealsig.Message("runtime", "runtime-secret", []byte("blob"))
	sig := sealsig.NewSigner(private).Sign(msg)
	expand := sealsig.NewVerifier([]ed25519.PublicKey{public}, false)
	contract := sealsig.NewVerifier([]ed25519.PublicKey{public}, true)
	if err := contract.Check(msg, sig); err != nil {
		t.Fatalf("a trusted signature = %v", err)
	}
	if err := expand.Check(sealsig.Message("runtime", "runtime-secret", []byte("other")), sig); !errors.Is(err, sealsig.ErrBadSignature) {
		t.Fatalf("a signature over another blob = %v", err)
	}
	if err := expand.Check(msg, sealsig.NewSigner(other).Sign(msg)); !errors.Is(err, sealsig.ErrBadSignature) {
		t.Fatalf("an untrusted signer = %v", err)
	}
	if err := expand.Check(msg, nil); err != nil {
		t.Fatalf("unsigned before the contract step = %v", err)
	}
	if err := contract.Check(msg, nil); !errors.Is(err, sealsig.ErrUnsigned) {
		t.Fatalf("unsigned after the contract step = %v", err)
	}
	var none *sealsig.Verifier
	if err := none.Check(msg, []byte("x")); err != nil {
		t.Fatalf("a nil verifier = %v", err)
	}
	var unsigned *sealsig.Signer
	if unsigned.Sign(msg) != nil {
		t.Fatal("a nil signer signed")
	}
}
