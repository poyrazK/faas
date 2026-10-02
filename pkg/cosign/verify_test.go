// verify_test.go — pkg/cosign.VerifyImageSignature + TrustedPublishersFromDir
// round-trip of ADR-058's legacy raw primitive and mirrored keys. Registry
// deployment verification now uses ADR-431 simple-signing attachments, tested
// in attached_image_test.go; these raw signatures are not that wire format.
//
// The shape under test:
//   - manifest digest  → 32-byte raw SHA-256 bytes
//   - signature payload → 64 bytes (P-256 r||s) over those 32 bytes
//   - trusted publisher  → ECDSA P-256 *ecdsa.PublicKey loaded from
//     a .pem on disk (mode 0444)
//   - missing signature from the supplied puller → ErrSignatureMissing
//   - wrong key / no match          → ErrSignatureInvalid
//
// What this file does NOT cover:
//   - LocalVerifier (build-side cold-boot) — covered by cosign_test.go
//   - LocalSigner — same file
//   - Registry transport — covered by oci and imaged attachment tests
package cosign

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePuller is a unit-test stub of ImageSignaturePuller. Both
// methods take refs by string and return canned data; production
// hands the oci.Puller wrapper (pkg/imaged/handler.go::
// ociImageSignaturePuller) instead. MissingSig=true simulates a
// registry with no cosign signature for the digest.
type fakePuller struct {
	// digestFor maps ref → canonical sha256:<hex>
	digestFor map[string]string
	// sigFor maps (ref, digest) → 64-byte signature bytes. Looked
	// up only when MissingSig is false for that ref.
	sigFor map[string][]byte
	// missingSigFor marks specific refs as having no signature
	// blob at the registry; FetchSignature returns ErrSignatureMissing
	// instead of looking up sigFor.
	missingSigFor map[string]bool
}

func (f *fakePuller) ResolveDigest(_ context.Context, ref string) (string, error) {
	d, ok := f.digestFor[ref]
	if !ok {
		return "", errors.New("fakePuller: unknown ref")
	}
	return d, nil
}

func (f *fakePuller) FetchSignature(_ context.Context, ref, _ string) ([]byte, error) {
	if f.missingSigFor[ref] {
		return nil, ErrSignatureMissing
	}
	sig, ok := f.sigFor[ref]
	if !ok {
		return nil, ErrSignatureMissing
	}
	return sig, nil
}

// signDigestOverWire computes the canonical cosign v2 signature:
// 64 bytes of P-256 r||s over the 32-byte digest bytes directly
// (NOT over sha256(digest) — verifyDigest forbids the re-hash).
// Mirrors LocalSigner.Sign's wire output so a happy-path test
// here exercises the exact shape the registry hands to imaged.
//
// Why not call signRaw? signRaw internally hashes its payload
// (sha256.Sum256(payload)), so calling signRaw(priv, digest[:])
// would double-hash: it'd sign sha256(sha256(manifest)), not the
// manifest digest itself. That's why we go straight to
// ecdsa.Sign — the test mirrors the verify side, which also
// calls ecdsa.Verify directly on the digest bytes.
func signDigestOverWire(t *testing.T, priv *ecdsa.PrivateKey, digest [32]byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	out := make([]byte, 64)
	rb := r.Bytes()
	sb := s.Bytes()
	if len(rb) > 32 || len(sb) > 32 {
		t.Fatalf("oversized r/s (%d/%d)", len(rb), len(sb))
	}
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out
}

// TestVerifyImageSignature_HappyPath: one trusted publisher, one
// image ref, signature round-trip succeeds → matchedSigner is the
// publisher name, err is nil.
func TestVerifyImageSignature_HappyPath(t *testing.T) {
	priv, _ := ecdsaP256KeyPair(t)
	digest := sha256OfBytes([]byte("manifest-bytes"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])

	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: signDigestOverWire(t, priv, digest)},
	}
	pubs := []TrustedPublisher{{Name: "ci-bot", PublicKey: &priv.PublicKey}}

	matched, gotDigest, err := VerifyImageSignature(context.Background(), puller, ref, pubs)
	if err != nil {
		t.Fatalf("VerifyImageSignature: %v", err)
	}
	if matched != "ci-bot" {
		t.Errorf("matched = %q, want ci-bot", matched)
	}
	if gotDigest != "sha256:"+hexDigest(digest[:]) {
		t.Errorf("digest = %q, want canonical sha256", gotDigest)
	}
}

// TestVerifyImageSignature_Missing: registry has no signature
// artifact → ErrSignatureMissing (distinct sentinel so the imaged
// audit can label the failure mode as "registry lacks sig" rather
// than "sig doesn't match").
func TestVerifyImageSignature_Missing(t *testing.T) {
	priv, _ := ecdsaP256KeyPair(t)
	digest := sha256OfBytes([]byte("manifest-bytes"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])

	puller := &fakePuller{
		digestFor:     map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:        map[string][]byte{},
		missingSigFor: map[string]bool{ref: true},
	}
	pubs := []TrustedPublisher{{Name: "ci-bot", PublicKey: &priv.PublicKey}}

	_, _, err := VerifyImageSignature(context.Background(), puller, ref, pubs)
	if !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("err = %v, want ErrSignatureMissing", err)
	}
}

// TestVerifyImageSignature_WrongKey: signature was made under a
// different key (the "attacker substituted the pub" failure mode).
// The trust list has the WRONG pub; the verifier must reject with
// ErrSignatureInvalid and walk the full publisher list (not fail
// on the first mismatch).
func TestVerifyImageSignature_WrongKey(t *testing.T) {
	signerPriv, _ := ecdsaP256KeyPair(t)
	_, otherPub := ecdsaP256KeyPair(t) // ← trust-list key
	digest := sha256OfBytes([]byte("manifest-bytes"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])

	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: signDigestOverWire(t, signerPriv, digest)},
	}
	// Trust list has a key that doesn't match the signer.
	pubs := []TrustedPublisher{
		{Name: "ci-bot-wrong", PublicKey: otherPub},
		{Name: "ci-bot", PublicKey: otherPub},
	}

	_, _, err := VerifyImageSignature(context.Background(), puller, ref, pubs)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

// TestVerifyImageSignature_NoPublishers: defence-in-depth check —
// even if the apid pre-flight is bypassed, VerifyImageSignature
// rejects the deploy with ErrSignatureInvalid when the trust list
// is empty. This catches "imaged called outside the apid pipeline"
// failure modes (e.g. a future bulk-decode path).
func TestVerifyImageSignature_NoPublishers(t *testing.T) {
	digest := sha256OfBytes([]byte("manifest-bytes"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])
	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: make([]byte, 64)},
	}

	_, _, err := VerifyImageSignature(context.Background(), puller, ref, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid (fail-closed)", err)
	}
}

// TestVerifyImageSignature_PicksCorrectPublisher: two publishers
// in the trust list, signature was made under the SECOND one. The
// verifier walks the list in order and returns the FIRST matching
// name — operators can use the trust list ordering to set a
// preferred signer (e.g. "ci-bot" before "legacy-bot").
func TestVerifyImageSignature_PicksCorrectPublisher(t *testing.T) {
	priv1, _ := ecdsaP256KeyPair(t)
	priv2, _ := ecdsaP256KeyPair(t)
	digest := sha256OfBytes([]byte("manifest-bytes"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])

	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: signDigestOverWire(t, priv2, digest)},
	}
	pubs := []TrustedPublisher{
		{Name: "ci-bot-1", PublicKey: &priv1.PublicKey},
		{Name: "ci-bot-2", PublicKey: &priv2.PublicKey},
	}

	matched, _, err := VerifyImageSignature(context.Background(), puller, ref, pubs)
	if err != nil {
		t.Fatalf("VerifyImageSignature: %v", err)
	}
	if matched != "ci-bot-2" {
		t.Errorf("matched = %q, want ci-bot-2 (only matching publisher)", matched)
	}
}

// TestTrustedPublishersFromDir: multi-PEM directory. Each .pem
// becomes a TrustedPublisher{Name=filename-without-extension}.
// Non-.pem files are ignored. Missing dir returns (nil, nil)
// — the apid pre-flight is the canonical fail-closed gate.
func TestTrustedPublishersFromDir(t *testing.T) {
	dir := t.TempDir()
	// Three valid PEM files.
	for _, name := range []string{"ci-bot", "manual", "legacy"} {
		_, pubPEM := generateP256PEM(t)
		full := filepath.Join(dir, name+".pem")
		if err := os.WriteFile(full, pubPEM, 0o444); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// One junk file the loader should ignore.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# trust list"), 0o444); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	// One sub-directory the loader should ignore.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := TrustedPublishersFromDir(dir)
	if err != nil {
		t.Fatalf("TrustedPublishersFromDir: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Name set matches (order isn't promised — sort before compare).
	gotNames := map[string]bool{}
	for _, p := range got {
		gotNames[p.Name] = true
		if p.PublicKey == nil {
			t.Errorf("publisher %q has nil PublicKey", p.Name)
		}
	}
	for _, want := range []string{"ci-bot", "manual", "legacy"} {
		if !gotNames[want] {
			t.Errorf("missing publisher %q in %v", want, gotNames)
		}
	}
}

// TestTrustedPublishersFromDir_Missing: a fresh install has the
// dir absent. TrustedPublishersFromDir must NOT crash — it returns
// (nil, nil) so imaged can keep running with an empty trust list.
// The apid pre-flight gate is what fail-closes an actual deploy.
func TestTrustedPublishersFromDir_Missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	got, err := TrustedPublishersFromDir(missing)
	if err != nil {
		t.Errorf("err = %v, want nil (missing dir is non-fatal)", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

// TestTrustedPublishersFromDir_BadPerms: a .pem with mode 0644
// must surface the existing LoadPublicKeyFile perm error — the
// "pub key tampered" signal from keys.go must not be silently
// swallowed by the directory loader.
func TestTrustedPublishersFromDir_BadPerms(t *testing.T) {
	dir := t.TempDir()
	_, pubPEM := generateP256PEM(t)
	full := filepath.Join(dir, "ci-bot.pem")
	if err := os.WriteFile(full, pubPEM, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := TrustedPublishersFromDir(dir)
	if !errors.Is(err, ErrInsecurePubKeyPerms) {
		t.Errorf("err = %v, want ErrInsecurePubKeyPerms", err)
	}
	if err != nil && !strings.Contains(err.Error(), "ci-bot.pem") {
		t.Errorf("err = %v, want ci-bot.pem in detail", err)
	}
}

// TestDigestBytesFromDigest: pin the sha256: → 32 raw bytes
// conversion. Off-by-one in the hex slice would silently accept
// wrong-length inputs; pin both the happy path and the error
// branches.
func TestDigestBytesFromDigest(t *testing.T) {
	const canonical = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	got, err := digestBytesFromDigest(canonical)
	if err != nil {
		t.Fatalf("digestBytesFromDigest: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("len = %d, want 32", len(got))
	}
	for i, b := range got {
		if b != 0 {
			t.Errorf("byte %d = %#x, want 0", i, b)
		}
	}
	// Wrong prefix.
	if _, err := digestBytesFromDigest("sha512:00"); err == nil {
		t.Error("want error for sha512: prefix")
	}
	// Wrong hex length.
	if _, err := digestBytesFromDigest("sha256:abcd"); err == nil {
		t.Error("want error for short hex")
	}
	// Invalid hex char.
	if _, err := digestBytesFromDigest("sha256:zz000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("want error for invalid hex")
	}
}

// --- small test helpers (kept local so the file compiles without
// pulling crypto/sha256, encoding/hex directly into the import set;
// pkg/cosign already pulls these in transitively, but explicit
// imports are clearer at the call site). ---

func ecdsaP256KeyPair(t *testing.T) (*ecdsa.PrivateKey, *ecdsa.PublicKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(ecdsaP256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	return priv, &priv.PublicKey
}

func sha256OfBytes(b []byte) [32]byte {
	return sha256.Sum256(b)
}

func hexDigest(b []byte) string {
	return hex.EncodeToString(b)
}

// generateP256PEM is a thin wrapper around GenerateKeyPair — it
// returns only the public PEM, which is all the directory loader
// needs. We don't reuse keyPairTempDir (it writes 0400 priv files
// the loader doesn't use).
func generateP256PEM(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()
	privPEM, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return privPEM, pubPEM
}
