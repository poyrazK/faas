// Package cosign is the platform's build-attestation primitive (ADR-038,
// Tier 3 Phase 3). It signs every ext4 app layer produced by imaged and
// every base ext4 staged at startup, and verifies them on cold-boot
// before schedd hands the layer spec to vmmd.
//
// Shape (intentionally NOT the full sigstore cosign CLI bundle):
//
//   - signer.go   — ECDSA P-256 over SHA-256(ext4 bytes). The secret
//     half lives at /etc/faas/secrets/sign.key (PEM-encoded PKCS#8,
//     mode 0400). The signature blob is a raw (r || s) concatenation
//     (P-256 is fixed-length so no DER prefix is needed), stored at
//     sigs/<layer-key>.sig via the existing StorageBackend.
//   - verifier.go — same ECDSA verification on cold-boot. Mismatch or
//     missing sig → *api.Problem with code=sig_invalid; schedd
//     transitions the deployment to DeployFailed.
//
// Why not the full cosign CLI / sigstore-go bundle? See
// docs/adr/038-build-attestation.md §Rejected alternatives. The spec
// only requires a tamper detector on cold-boot — sigstore-go's
// digest+ECDSA primitive covers that with zero cosign-CLI weight in
// the build tree. KMS-backed signing (the only thing full cosign
// would unlock today) is ADR-039 / Phase 4.
//
// Key custody:
//
//   - /etc/faas/secrets/sign.key     — ECDSA P-256 PKCS#8 (mode 0400)
//   - /etc/faas/secrets/sign-pub.pem — ECDSA P-256 SubjectPublicKeyInfo
//     (mode 0444)
//   - Missing key → cmd/imaged and cmd/schedd refuse to start. This
//     is the deliberate "no silent insecure boots" stance (ADR-038
//     §Consequences Compatibility).
package cosign

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Signer writes a signature blob for an artifact under a derived
// sig key. Implementations are expected to be thread-safe (called
// from many imaged Build goroutines concurrently per the spec §4.6
// per-app layer write path).
type Signer interface {
	// Sign reads the artifact at layerKey, signs its bytes with the
	// platform's ECDSA P-256 keypair, and writes the raw signature
	// to sigKey via Storage.Put. Returns an error if the read,
	// signing, or write fails; the caller surfaces this to the
	// build pipeline as a build failure (signing failures are
	// wake-blocking for the produced layer).
	Sign(ctx context.Context, layerKey, sigKey string) error
}

// Verifier checks that the signature at sigKey is a valid ECDSA
// signature over the bytes at layerKey under the platform's public
// key. Returns nil on success, *api.Problem with code=sig_invalid
// on mismatch, or the error chain from the storage backend on I/O
// failure.
type Verifier interface {
	Verify(ctx context.Context, layerKey, sigKey string) error
}

// SigKeyFor returns the canonical sig key for an artifact key.
// Caller convention: app-layer sigs at "sigs/apps/<slug>/<dep-id>.sig",
// base sigs at "sigs/base/<arch>/<name>.sig". The exact shape is
// up to the caller — this package only requires the keys be
// non-empty and pass storage.validateKey.
// Regional artifacts keep their regions/<id>/ namespace around this shape.
func SigKeyFor(layerKey string) string {
	return region.SignatureKey(layerKey)
}

// signRaw returns the ECDSA P-256 (r || s) signature over the SHA-256
// digest of payload. P-256 r and s are each 32 bytes; the output is
// always 64 bytes. Exposed (lowercase) for tests; production callers
// use LocalSigner.
func signRaw(priv *ecdsa.PrivateKey, payload []byte) ([]byte, error) {
	digest := sha256.Sum256(payload)
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return nil, fmt.Errorf("cosign: sign: %w", err)
	}
	out := make([]byte, 64)
	rb := r.Bytes()
	sb := s.Bytes()
	// Left-pad to 32 bytes each; r/s may be < 32 bytes for values
	// whose top bits are zero (rare but legal per FIPS 186-4).
	if len(rb) > 32 || len(sb) > 32 {
		return nil, fmt.Errorf("cosign: oversized r/s (%d/%d)", len(rb), len(sb))
	}
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out, nil
}

// verifyDigest checks sig is a valid 64-byte ECDSA P-256 (r||s)
// signature over the 32-byte SHA-256 digest under pub.
//
// The digest is pre-computed by the caller (LocalVerifier hashes
// the streamed artifact, then passes the 32-byte digest here).
// Verifying over the digest — not over `sha256(digest)` — is the
// load-bearing invariant: the signer signs the digest bytes
// directly via ecdsa.Sign(rand, priv, digest[:]) (see LocalSigner.Sign)
// and the verifier must NOT re-hash before ecdsa.Verify.
func verifyDigest(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	if len(digest) != 32 {
		return false
	}
	r := newBigInt(sig[:32])
	s := newBigInt(sig[32:])
	return ecdsa.Verify(pub, digest, r, s)
}

// LocalSigner is the production signer: reads the artifact from
// Storage, signs with the on-disk PKCS#8 key, writes the raw
// signature back to Storage at the sig key.
//
// Constructed once at daemon startup; the underlying *ecdsa.PrivateKey
// is held in memory for the lifetime of the process. log is optional;
// pass nil in unit tests, the daemon's slog.Logger at startup.
type LocalSigner struct {
	priv *ecdsa.PrivateKey
	stor storage.StorageBackend
	log  Logger
}

// Logger is a minimal interface for the signer to log non-fatal
// close errors. Implemented by *slog.Logger so we don't drag the
// whole slog import set into pkg/cosign just for one Warn call.
type Logger interface {
	Warn(msg string, args ...any)
}

// NewLocalSigner parses the PKCS#8 PEM at path (mode 0400 enforced
// by LoadPrivateKeyFile inside loadPrivateKey — LoadPrivateKeyFile
// is what cmd/imaged calls at startup) and wires a LocalSigner that
// signs artifacts read from / written to stor. Returns an error if
// the PEM is missing, malformed, or not ECDSA P-256.
func NewLocalSigner(path string, stor storage.StorageBackend, log Logger) (*LocalSigner, error) {
	priv, err := LoadPrivateKeyFile(path)
	if err != nil {
		return nil, err
	}
	if stor == nil {
		return nil, errors.New("cosign: NewLocalSigner: nil storage backend")
	}
	return &LocalSigner{priv: priv, stor: stor, log: log}, nil
}

// Sign reads layerKey, computes ECDSA-P-256 over SHA-256(bytes),
// writes the raw 64-byte signature to sigKey.
func (s *LocalSigner) Sign(ctx context.Context, layerKey, sigKey string) error {
	if layerKey == "" || sigKey == "" {
		return errors.New("cosign: Sign: empty layerKey or sigKey")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cosign: pre-sign ctx: %w", err)
	}
	rc, err := s.stor.Get(ctx, layerKey)
	if err != nil {
		return fmt.Errorf("cosign: read layer %q: %w", layerKey, err)
	}
	// Hash the artifact without slurping it into memory: ext4s can
	// be 1-2 GB (Pro/Scale apps with large layers); the SHA-256 +
	// ECDSA sign both operate on the digest. We stream the hash
	// and then sign the digest.
	h := sha256.New()
	// Wrap the Reader in a ctx-aware copy loop: io.Copy
	// doesn't natively check ctx, but the storage backend's
	// Read typically honors the context passed to Get — and a
	// 1-2 GB hash on a piped stream would happily finish
	// after the caller cancelled.
	cc := newCtxCopy(h, rc, ctx)
	n, err := cc.Do()
	if err != nil {
		_ = rc.Close()
		return fmt.Errorf("cosign: hash layer %q: %w", layerKey, err)
	}
	_ = n // suppress unused warning; cc reports bytes copied
	// Observe the close error: a mid-read truncation silently
	// corrupts the digest (possible on remote / OBS backends).
	// Log via the daemon's slog; LocalSigner is constructed once
	// at startup so the optional logger is non-nil in production
	// and nil in unit tests (test signer passes nil; the call
	// is no-op).
	if closeErr := rc.Close(); closeErr != nil {
		if s.log != nil {
			s.log.Warn("cosign: close layer reader after hash",
				"layer", layerKey, "err", closeErr)
		}
	}
	digest := h.Sum(nil)
	r, sBig, err := ecdsa.Sign(rand.Reader, s.priv, digest)
	if err != nil {
		return fmt.Errorf("cosign: ecdsa sign: %w", err)
	}
	out := make([]byte, 64)
	rb := r.Bytes()
	sb := sBig.Bytes()
	if len(rb) > 32 || len(sb) > 32 {
		return fmt.Errorf("cosign: oversized r/s (%d/%d)", len(rb), len(sb))
	}
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)

	// Write the raw sig blob via bytes.NewReader. One heap alloc
	// per Sign — well under the build-pipeline cost budget (one
	// Sign per build, not per wake). Avoids the byteReader
	// pointer-receiver footgun a value-receiver use would
	// reintroduce (Storage.Put's io.ReadAll loops forever if the
	// slice header doesn't advance between Read calls).
	if err := s.stor.Put(ctx, sigKey, bytes.NewReader(out)); err != nil {
		return fmt.Errorf("cosign: write sig %q: %w", sigKey, err)
	}
	return nil
}

// loadPrivateKey parses a PEM-encoded PKCS#8 ECDSA private key. The
// key is required to be on the P-256 curve (ADR-038 §Decision).
func loadPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("cosign: empty key path")
	}
	// File read + mode check happen in the daemon's cmd/* wiring
	// (the platform convention — secretbox.LoadHostKey does the
	// same; LoadPublicKey below enforces 0444). Here we only parse.
	raw, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("cosign: read private key %q: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("cosign: %q is not PEM-encoded", path)
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("cosign: %q: want PEM type PRIVATE KEY, got %q", path, block.Type)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cosign: parse PKCS8 %q: %w", path, err)
	}
	priv, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("cosign: %q is not ECDSA (got %T)", path, k)
	}
	if priv.Curve != ecdsaP256() {
		return nil, fmt.Errorf("cosign: %q: want P-256 curve, got %s", path, priv.Curve.Params().Name)
	}
	return priv, nil
}
