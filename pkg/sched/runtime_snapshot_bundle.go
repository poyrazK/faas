package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// RuntimeSnapshotBundleDigest hashes the identity and the length and SHA-256
// of memory, machine state, frozen writable drive and backing identity, in
// that order. Reads are bounded; no memory image is buffered. The backing
// must name the release's actual kernel/base, not just exist as a sidecar.
func RuntimeSnapshotBundleDigest(ctx context.Context, backend storage.StorageBackend, snapshot RuntimeSnapshot) (string, error) {
	if backend == nil {
		return "", ErrRuntimeSnapshotUnwired
	}
	key, err := snapshot.Identity.Key()
	if err != nil || snapshot.Identity.FormatVersion != 2 || !executionTemplateStorageKey(snapshot.StorageKey) {
		return "", errors.Join(ErrRuntimeSnapshotCorrupt, err)
	}
	if snapshot.MemBytes != int64(snapshot.Identity.MemoryMB)<<20 || snapshot.VMStateBytes <= 0 || snapshot.VMStateBytes > api.ExecutionTemplateVMStateMaxBytes {
		return "", ErrRuntimeSnapshotCorrupt
	}
	snap := state.Snapshot{StorageKey: snapshot.StorageKey}
	parts := []struct {
		name, key  string
		exact, max int64
	}{
		{"mem", snap.StorageKey, snapshot.MemBytes, snapshot.MemBytes},
		{"vmstate", state.SnapshotVMStateKey(snap), snapshot.VMStateBytes, snapshot.VMStateBytes},
		{"drive", state.SnapshotDriveKey(snap), 0, int64(snapshot.Identity.EphemeralDiskMB) << 20},
		{"backing", state.SnapshotBackingKey(snap), 0, api.ExecutionTemplateBackingMaxBytes},
	}
	h := sha256.New()
	_, _ = io.WriteString(h, "gregale.execution.runtime-snapshot.v2\x00"+key+"\x00")
	for _, part := range parts {
		reader, err := backend.Get(ctx, part.key)
		if err != nil {
			if storage.IsNotFound(err) {
				err = errors.Join(ErrRuntimeSnapshotCorrupt, err)
			}
			return "", fmt.Errorf("sched: runtime template %s: %w", part.name, err)
		}
		if reader == nil {
			return "", ErrRuntimeSnapshotCorrupt
		}
		sum := sha256.New()
		var body strings.Builder
		var dst io.Writer = sum
		if part.name == "backing" {
			dst = io.MultiWriter(sum, &body)
		}
		n, copyErr := copyTemplatePart(ctx, dst, reader, part.max)
		closeErr := reader.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return "", fmt.Errorf("sched: runtime template %s: %w", part.name, err)
		}
		if n <= 0 || part.exact > 0 && n != part.exact {
			return "", ErrRuntimeSnapshotCorrupt
		}
		if part.name == "backing" {
			var backing struct {
				Version             int
				Kernel, Base, Timer string
			}
			if json.Unmarshal([]byte(body.String()), &backing) != nil || backing.Version != 2 || backing.Kernel != "sha256:"+snapshot.Identity.KernelDigest || backing.Base != "sha256:"+snapshot.Identity.BaseImageDigest || backing.Timer == "" {
				return "", ErrRuntimeSnapshotCorrupt
			}
		}
		writeRuntimeSnapshotDigestHeader(h, part.name, n)
		_, _ = h.Write(sum.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyTemplatePart(ctx context.Context, dst io.Writer, src io.Reader, max int64) (int64, error) {
	src = io.LimitReader(src, max+1)
	buf := make([]byte, 128<<10)
	var total int64
	empty := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > max {
				return total, ErrRuntimeSnapshotCorrupt
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				return total, err
			}
			empty = 0
		} else {
			empty++
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		if empty >= 100 {
			return total, io.ErrNoProgress
		}
	}
}

func executionTemplateStorageKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 6 || parts[0] != "snap" || parts[1] != uuid.Nil.String() || parts[2] != "captures" || parts[4] != "v2" || parts[5] != "mem" {
		return false
	}
	id, err := uuid.Parse(parts[3])
	return err == nil && id != uuid.Nil && id.String() == parts[3]
}
