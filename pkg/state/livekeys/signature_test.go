package livekeys_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// A writer signature names the row and recipient it was made for, so a
// signed value moved to another database, another key ID or another
// recipient no longer verifies, and a value no trusted key signed is refused.
// An unsigned value passes until the contract step requires signatures.
func TestWriterSignatureBindsTheRow(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const db, kid, recipient = "3f1c0b6e-9a2b-44d1-8c7e-0123456789ab", "aaaaaaaaaaaaaaaa", "age1recipient"
	blob := []byte("sealed")
	sig := livekeys.NewSigner(private).Sign(livekeys.SignatureKindKey, db, kid, recipient, blob)
	verify := livekeys.NewVerifier([]ed25519.PublicKey{otherPublic, public}, false)

	if err := verify.Check(livekeys.SignatureKindKey, db, kid, recipient, blob, sig); err != nil {
		t.Fatalf("the writer's own signature = %v", err)
	}
	for name, check := range map[string]error{
		"another database":  verify.Check(livekeys.SignatureKindKey, "0a1b2c3d-0000-4000-8000-000000000001", kid, recipient, blob, sig),
		"another key ID":    verify.Check(livekeys.SignatureKindKey, db, "bbbbbbbbbbbbbbbb", recipient, blob, sig),
		"another recipient": verify.Check(livekeys.SignatureKindKey, db, kid, "age1other", blob, sig),
		"another kind":      verify.Check(livekeys.SignatureKindLogin, db, kid, recipient, blob, sig),
		"another blob":      verify.Check(livekeys.SignatureKindKey, db, kid, recipient, []byte("forged"), sig),
		"an untrusted key": verify.Check(livekeys.SignatureKindKey, db, kid, recipient, blob,
			livekeys.NewSigner(otherPrivate).Sign(livekeys.SignatureKindKey, db, kid, recipient, []byte("x"))),
	} {
		if !errors.Is(check, livekeys.ErrBadSignature) {
			t.Errorf("%s: %v, want ErrBadSignature", name, check)
		}
	}
	if err := verify.Check(livekeys.SignatureKindKey, db, kid, recipient, blob, nil); err != nil {
		t.Fatalf("an unsigned value before the contract step = %v", err)
	}
	required := livekeys.NewVerifier([]ed25519.PublicKey{public}, true)
	if err := required.Check(livekeys.SignatureKindKey, db, kid, recipient, blob, nil); !errors.Is(err, livekeys.ErrUnsigned) {
		t.Fatalf("an unsigned value after the contract step = %v, want ErrUnsigned", err)
	}
	var none *livekeys.Verifier
	if err := none.Check(livekeys.SignatureKindKey, db, kid, recipient, blob, []byte("anything")); err != nil {
		t.Fatalf("a nil verifier = %v", err)
	}
	var unsigned *livekeys.Signer
	if got := unsigned.Sign(livekeys.SignatureKindKey, db, kid, recipient, blob); got != nil {
		t.Fatalf("a nil signer signed: %x", got)
	}
}

// Parts are length-prefixed: moving bytes from one part to the next changes
// the message.
func TestSignatureMessageSeparatesParts(t *testing.T) {
	a := livekeys.SignatureMessage(livekeys.SignatureKindKey, "ab", "c", "r", []byte("b"))
	b := livekeys.SignatureMessage(livekeys.SignatureKindKey, "a", "bc", "r", []byte("b"))
	if string(a) == string(b) {
		t.Fatal("two different rows produced the same signature message")
	}
}

func TestSigningKeyFilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	signPath, verifyPath := filepath.Join(dir, "signing.key"), filepath.Join(dir, "signing.pub")
	if err := livekeys.WriteSigningKeyPair(signPath, verifyPath); err != nil {
		t.Fatal(err)
	}
	if err := livekeys.WriteSigningKeyPair(signPath, verifyPath); err == nil {
		t.Fatal("an existing key pair was overwritten")
	}
	if info, err := os.Stat(signPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	private, err := livekeys.LoadSigningKey(signPath)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := livekeys.LoadVerifyKeys(verifyPath)
	if err != nil || len(keys) != 1 {
		t.Fatalf("verify keys = %d, %v", len(keys), err)
	}
	sig := livekeys.NewSigner(private).Sign(livekeys.SignatureKindLogin, "db", "", "r", []byte("blob"))
	if err := livekeys.NewVerifier(keys, true).Check(livekeys.SignatureKindLogin, "db", "", "r", []byte("blob"), sig); err != nil {
		t.Fatalf("a signature by the generated key = %v", err)
	}
	if _, err := livekeys.LoadVerifyKeys(signPath); err == nil {
		t.Fatal("a private key file loaded as verify keys")
	}
}
