package cosign

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// LocalVerifier is the production verifier: reads the artifact +
// signature from Storage, checks ECDSA-P-256 over SHA-256(artifact)
// against the on-disk PEM public key.
//
// Constructed once at daemon startup; the underlying *ecdsa.PublicKey
// is held in memory for the lifetime of the process. schedd
// invokes Verify on the two wake sites (engine.go:481 + :823) —
// cold-boot + prime — before handing the AppSpec to vmmd.
type LocalVerifier struct {
	pub      *ecdsa.PublicKey
	stor     storage.StorageBackend
	verified sync.Map
}

// NewLocalVerifier parses the SPKI PEM at path (mode ≤ 0o444
// enforced by LoadPublicKeyFile) and wires a LocalVerifier that
// reads both layer and signature blobs from stor. Returns an
// error if the PEM is missing, malformed, or not ECDSA P-256.
func NewLocalVerifier(path string, stor storage.StorageBackend) (*LocalVerifier, error) {
	pub, err := LoadPublicKeyFile(path)
	if err != nil {
		return nil, err
	}
	if stor == nil {
		return nil, errors.New("cosign: NewLocalVerifier: nil storage backend")
	}
	return &LocalVerifier{pub: pub, stor: stor}, nil
}

// CheckPresent is the request-path precheck for an artifact that a later
// boot will Verify in full. It reads the 64-byte signature and probes the
// layer's existence without transferring the layer body, so it fits inside
// an HTTP deadline where hashing a multi-GB rootfs from a remote registry
// cannot (issue #3356). It does not prove integrity: schedd's prime and
// cold-boot paths still run Verify before any boot.
//
// Returns a storage.ErrNotFound-wrapping error when either object is
// missing, the same sig_invalid problem as Verify for a malformed
// signature, and plain I/O errors otherwise.
func (v *LocalVerifier) CheckPresent(ctx context.Context, layerKey, sigKey string) error {
	if layerKey == "" || sigKey == "" {
		return errors.New("cosign: CheckPresent: empty layerKey or sigKey")
	}
	sigRC, err := v.stor.Get(ctx, sigKey)
	if err != nil {
		return fmt.Errorf("cosign: read sig %q: %w", sigKey, err)
	}
	sig, err := io.ReadAll(io.LimitReader(sigRC, 65))
	_ = sigRC.Close()
	if err != nil {
		return fmt.Errorf("cosign: read sig %q: %w", sigKey, err)
	}
	if len(sig) != 64 {
		return api.NewProblem(503, "sig_invalid",
			"malformed signature for cold-boot layer",
			fmt.Sprintf("signature for layer %q at %q is %d bytes, want 64", layerKey, sigKey, len(sig)))
	}
	exists, supported, err := storage.Exists(ctx, v.stor, layerKey)
	if err != nil {
		return fmt.Errorf("cosign: probe layer %q: %w", layerKey, err)
	}
	if supported && !exists {
		return fmt.Errorf("cosign: layer %q: %w", layerKey, storage.ErrNotFound)
	}
	return nil
}

// Verify reads the layer + sig, hashes the layer, and checks the
// signature. Returns nil on success. On mismatch or missing sig,
// returns *api.Problem with code=sig_invalid (HTTP 503, per
// ADR-038 §Consequences). I/O errors from the storage backend
// bubble up unwrapped — schedd surfaces them as a transient
// wake failure (the caller decides whether to retry).
func (v *LocalVerifier) Verify(ctx context.Context, layerKey, sigKey string) error {
	if layerKey == "" || sigKey == "" {
		return errors.New("cosign: Verify: empty layerKey or sigKey")
	}
	cacheKey := layerKey + "\x00" + sigKey
	if _, ok := v.verified.Load(cacheKey); ok {
		return nil
	}

	// Hash the layer (streamed — ext4s can be 1-2 GB).
	h := sha256.New()
	layerRC, err := v.stor.Get(ctx, layerKey)
	if err != nil {
		return fmt.Errorf("cosign: read layer %q: %w", layerKey, err)
	}
	if _, err := io.Copy(h, layerRC); err != nil {
		_ = layerRC.Close()
		return fmt.Errorf("cosign: hash layer %q: %w", layerKey, err)
	}
	if err := layerRC.Close(); err != nil {
		return fmt.Errorf("cosign: close layer %q: %w", layerKey, err)
	}
	digest := h.Sum(nil)

	// Read the signature blob (64 bytes — P-256 r||s).
	sigRC, err := v.stor.Get(ctx, sigKey)
	if err != nil {
		if storage.IsNotFound(err) {
			// Title for human display; Detail carries the
			// operator-facing message verbatim so the
			// Problem.Error() string (which only includes
			// code + detail) names the failure mode for
			// log greps.
			return api.NewProblem(503, "sig_invalid",
				"signature missing for cold-boot layer",
				fmt.Sprintf("signature missing for layer %q at %q; refusing to boot unsigned ext4",
					layerKey, sigKey))
		}
		return fmt.Errorf("cosign: read sig %q: %w", sigKey, err)
	}
	sig, err := io.ReadAll(sigRC)
	if err != nil {
		_ = sigRC.Close()
		return fmt.Errorf("cosign: read sig %q: %w", sigKey, err)
	}
	if err := sigRC.Close(); err != nil {
		return fmt.Errorf("cosign: close sig %q: %w", sigKey, err)
	}

	if !verifyDigest(v.pub, digest, sig) {
		// Title for human display; Detail is the load-bearing
		// operator message (Problem.Error() returns code+detail,
		// so log greps will find this string verbatim).
		return api.NewProblem(503, "sig_invalid",
			"signature does not match ext4",
			fmt.Sprintf("signature does not match ext4: ECDSA P-256 verification failed for layer %q against sig %q; refusing to boot tampered ext4",
				layerKey, sigKey))
	}
	v.verified.Store(cacheKey, struct{}{})
	return nil
}
