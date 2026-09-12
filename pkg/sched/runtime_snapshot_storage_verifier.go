package sched

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/onebox-faas/faas/pkg/storage"
)

const runtimeSnapshotDigestDomain = "gregale.execution.runtime-snapshot.v1\x00"

// StorageRuntimeSnapshotVerifier authenticates both objects in a published
// runtime snapshot. SnapshotDigest is a digest of the length-delimited memory
// and vmstate pair, so changing either object makes the catalog entry a cache
// miss instead of a restore attempt.
type StorageRuntimeSnapshotVerifier struct {
	backend storage.StorageBackend
}

func NewStorageRuntimeSnapshotVerifier(backend storage.StorageBackend) *StorageRuntimeSnapshotVerifier {
	return &StorageRuntimeSnapshotVerifier{backend: backend}
}

var _ RuntimeSnapshotVerifier = (*StorageRuntimeSnapshotVerifier)(nil)

// VerifyRuntimeSnapshot streams the exact expected byte counts through a
// SHA-256 digest. It never buffers a snapshot in memory and caps reads at one
// byte beyond the catalogued size, preventing an oversized object from
// turning verification into an unbounded operation.
func (v *StorageRuntimeSnapshotVerifier) VerifyRuntimeSnapshot(ctx context.Context, snapshot RuntimeSnapshot) error {
	if v == nil || v.backend == nil {
		return ErrRuntimeSnapshotUnwired
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("%w: metadata: %w", ErrRuntimeSnapshotCorrupt, err)
	}
	vmstateKey := runtimeSnapshotVMStateStorageKey(snapshot.StorageKey)
	h := sha256.New()
	_, _ = io.WriteString(h, runtimeSnapshotDigestDomain)
	if err := verifySnapshotObject(ctx, v.backend, h, "mem", snapshot.StorageKey, snapshot.MemBytes); err != nil {
		return err
	}
	if err := verifySnapshotObject(ctx, v.backend, h, "vmstate", vmstateKey, snapshot.VMStateBytes); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != snapshot.SnapshotDigest {
		return fmt.Errorf("%w: snapshot digest mismatch", ErrRuntimeSnapshotCorrupt)
	}
	return nil
}

// RuntimeSnapshotDigest returns the digest format expected by
// StorageRuntimeSnapshotVerifier. It is primarily useful to a trusted
// publication path and hermetic tests; publication must still set the
// sanitized and payload-free flags only after the capture has been scrubbed.
func RuntimeSnapshotDigest(memory, vmstate []byte) string {
	h := sha256.New()
	_, _ = io.WriteString(h, runtimeSnapshotDigestDomain)
	writeDigestPart(h, "mem", memory)
	writeDigestPart(h, "vmstate", vmstate)
	return hex.EncodeToString(h.Sum(nil))
}

func writeDigestPart(w io.Writer, name string, data []byte) {
	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, "\x00")
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = w.Write(size[:])
	_, _ = w.Write(data)
}

func verifySnapshotObject(ctx context.Context, backend storage.StorageBackend, h io.Writer, name, key string, expected int64) error {
	if expected <= 0 {
		return fmt.Errorf("%w: %s object has invalid expected size", ErrRuntimeSnapshotCorrupt, name)
	}
	reader, err := backend.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalidKey) {
		return fmt.Errorf("%w: %s object is unavailable", ErrRuntimeSnapshotCorrupt, name)
	}
	if err != nil {
		return fmt.Errorf("sched: read runtime snapshot %s object: %w", name, err)
	}
	if reader == nil {
		return fmt.Errorf("%w: %s object returned no reader", ErrRuntimeSnapshotCorrupt, name)
	}
	defer func() { _ = reader.Close() }()

	_, _ = io.WriteString(h, name)
	_, _ = io.WriteString(h, "\x00")
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(expected))
	if _, err := h.Write(size[:]); err != nil {
		return fmt.Errorf("sched: hash runtime snapshot %s object: %w", name, err)
	}
	if expected == math.MaxInt64 {
		return fmt.Errorf("%w: %s object size is too large", ErrRuntimeSnapshotCorrupt, name)
	}
	limited := io.LimitReader(reader, expected+1)
	var copied int64
	buf := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := limited.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return fmt.Errorf("sched: hash runtime snapshot %s object: %w", name, err)
			}
			copied += int64(n)
			if copied > expected {
				return fmt.Errorf("%w: %s object is oversized", ErrRuntimeSnapshotCorrupt, name)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fmt.Errorf("sched: read runtime snapshot %s object: %w", name, readErr)
		}
	}
	if copied != expected {
		return fmt.Errorf("%w: %s object size mismatch", ErrRuntimeSnapshotCorrupt, name)
	}
	return nil
}
