package cosign

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sync/singleflight"
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
	inflight singleflight.Group
	// records remembers verifications across restarts; nil keeps them in
	// memory only (RememberVerifications).
	records *attestationRecords
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

// RememberVerifications keeps successful verifications in path, so a
// restarted daemon does not re-hash layers whose node-cache file has not
// changed since it verified them (ADR-038 amendment, 2026-10-24). A record
// is trusted for maxAge. It returns an error, and keeps verifications in
// memory only, when path is not a file private to this process.
func (v *LocalVerifier) RememberVerifications(path string, maxAge time.Duration) error {
	keyID, err := publicKeyID(v.pub)
	if err != nil {
		return err
	}
	records, err := openAttestationRecords(path, keyID, maxAge, time.Now)
	if err != nil {
		return err
	}
	v.records = records
	return nil
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
	// Concurrent callers for one layer share a single read and hash: the
	// background attestation warm and the first wakes of an app after a
	// schedd restart otherwise each stream the whole layer on an already
	// busy node (hunt #6, H5-56). The shared verification is detached from
	// the caller that started it, so a wake that gives up neither aborts it
	// for the others nor wastes it; api.LayerVerifyTimeout bounds it.
	result := v.inflight.DoChan(cacheKey, func() (any, error) {
		verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.LayerVerifyTimeout)
		defer cancel()
		return nil, v.verify(verifyCtx, layerKey, sigKey, cacheKey)
	})
	select {
	case r := <-result:
		return r.Err
	case <-ctx.Done():
		return fmt.Errorf("cosign: verify layer %q: %w", layerKey, ctx.Err())
	}
}

func (v *LocalVerifier) verify(ctx context.Context, layerKey, sigKey, cacheKey string) error {
	layerRC, identity, local, err := v.openLayer(ctx, layerKey)
	if err != nil {
		return fmt.Errorf("cosign: read layer %q: %w", layerKey, err)
	}
	// The node-cache file this host verified before a restart: the same
	// file holds the same bytes, so the signature still covers them.
	if local && v.records != nil && v.records.matches(layerKey, sigKey, identity) {
		_ = layerRC.Close()
		v.verified.Store(cacheKey, struct{}{})
		return nil
	}
	// Hash the layer (streamed — ext4s can be 1-2 GB).
	h := sha256.New()
	if _, err := newCtxCopy(h, layerRC, ctx).Do(); err != nil {
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
	if local && v.records != nil {
		// Best effort: a record that cannot be written only means the next
		// restart hashes this layer again.
		_ = v.records.remember(layerKey, sigKey, identity)
	}
	v.verified.Store(cacheKey, struct{}{})
	return nil
}

// openLayer opens the layer's local node-cache file when the backend has one,
// reporting the file it opened, so the bytes hashed and the identity recorded
// belong to the same file even if the cache replaces it meanwhile. Otherwise
// it streams the layer from the backend.
func (v *LocalVerifier) openLayer(ctx context.Context, layerKey string) (io.ReadCloser, fileIdentity, bool, error) {
	if resolver, ok := v.stor.(storage.LocalPathResolver); ok {
		if path, local, err := resolver.LocalPath(layerKey); err == nil && local {
			// O_NOFOLLOW plus identityOf's regular-file check: only a
			// plain cache file is hashed or has its identity recorded.
			if f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0); err == nil {
				if fi, err := f.Stat(); err == nil {
					if id, ok := identityOf(fi); ok {
						return f, id, true, nil
					}
				}
				_ = f.Close()
			}
		}
	}
	rc, err := v.stor.Get(ctx, layerKey)
	return rc, fileIdentity{}, false, err
}
