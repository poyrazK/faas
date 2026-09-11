package state

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeSnapshotRecord is the state-owned projection of one sanitized
// platform runtime. It intentionally contains no tenant source, input,
// credentials, or mutable application state.
type RuntimeSnapshotRecord struct {
	ID                  string
	CatalogKey          string
	Runtime             api.ExecutionRuntime
	Architecture        string
	KernelDigest        string
	GuestExecutorDigest string
	BaseImageDigest     string
	MemoryMB            int
	EphemeralDiskMB     int
	FormatVersion       int
	StorageKey          string
	SnapshotDigest      string
	MemBytes            int64
	VMStateBytes        int64
	Sanitized           bool
	PayloadFree         bool
	State               string
	CreatedAt           time.Time
	PublishedAt         time.Time
	RetiredAt           *time.Time
}

const (
	RuntimeSnapshotStateReady   = "ready"
	RuntimeSnapshotStateRetired = "retired"
)

var ErrRuntimeSnapshotInvalid = errors.New("state: invalid runtime snapshot")

// RuntimeSnapshotStore is the durable publication and lookup boundary used
// by sched. Implementations must never replace an existing catalog key;
// retirement is the only lifecycle mutation.
type RuntimeSnapshotStore interface {
	PublishRuntimeSnapshot(ctx context.Context, record RuntimeSnapshotRecord) (RuntimeSnapshotRecord, error)
	LookupRuntimeSnapshot(ctx context.Context, catalogKey string) (RuntimeSnapshotRecord, error)
	RetireRuntimeSnapshot(ctx context.Context, catalogKey string, retiredAt time.Time) error
}

func (r RuntimeSnapshotRecord) identityKey() string {
	return fmt.Sprintf(
		"execution-snapshots/v%d/%s/%s/memory-%d/disk-%d/kernel-%s/executor-%s/base-%s",
		r.FormatVersion, r.Runtime, r.Architecture, r.MemoryMB, r.EphemeralDiskMB,
		r.KernelDigest, r.GuestExecutorDigest, r.BaseImageDigest,
	)
}

// Validate is the state-layer backstop for trusted publication. Keep this in
// sync with sched.RuntimeSnapshot.Validate; duplicating the small invariant
// here avoids an import cycle between state and sched.
func (r RuntimeSnapshotRecord) Validate() error {
	if !r.Runtime.Valid() {
		return fmt.Errorf("%w: unsupported runtime %q", ErrRuntimeSnapshotInvalid, r.Runtime)
	}
	if r.Architecture != "amd64" && r.Architecture != "arm64" {
		return fmt.Errorf("%w: unsupported architecture %q", ErrRuntimeSnapshotInvalid, r.Architecture)
	}
	for name, digest := range map[string]string{
		"kernel": r.KernelDigest, "executor": r.GuestExecutorDigest, "base": r.BaseImageDigest,
	} {
		if !runtimeSnapshotDigestValid(digest) {
			return fmt.Errorf("%w: %s digest must be lowercase sha256 hex", ErrRuntimeSnapshotInvalid, name)
		}
	}
	if !api.ValidExecutionMemoryMB(r.MemoryMB) || !api.ValidExecutionEphemeralDiskMB(r.EphemeralDiskMB) {
		return fmt.Errorf("%w: machine shape is outside execution limits", ErrRuntimeSnapshotInvalid)
	}
	if r.FormatVersion <= 0 {
		return fmt.Errorf("%w: format version must be positive", ErrRuntimeSnapshotInvalid)
	}
	if r.CatalogKey == "" || r.CatalogKey != r.identityKey() {
		return fmt.Errorf("%w: catalog key does not match identity", ErrRuntimeSnapshotInvalid)
	}
	if strings.TrimSpace(r.StorageKey) != r.StorageKey || !runtimeSnapshotStorageKeyValid(r.StorageKey) {
		return fmt.Errorf("%w: storage key is not canonical", ErrRuntimeSnapshotInvalid)
	}
	if !runtimeSnapshotDigestValid(r.SnapshotDigest) {
		return fmt.Errorf("%w: snapshot digest must be lowercase sha256 hex", ErrRuntimeSnapshotInvalid)
	}
	if r.MemBytes <= 0 || r.VMStateBytes <= 0 {
		return fmt.Errorf("%w: snapshot byte sizes must be positive", ErrRuntimeSnapshotInvalid)
	}
	if !r.Sanitized || !r.PayloadFree {
		return fmt.Errorf("%w: snapshot is not proven sanitized and payload-free", ErrRuntimeSnapshotInvalid)
	}
	if r.State != RuntimeSnapshotStateReady && r.State != RuntimeSnapshotStateRetired {
		return fmt.Errorf("%w: unsupported state %q", ErrRuntimeSnapshotInvalid, r.State)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrRuntimeSnapshotInvalid)
	}
	if !r.PublishedAt.IsZero() && r.PublishedAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: published_at precedes created_at", ErrRuntimeSnapshotInvalid)
	}
	if (r.State == RuntimeSnapshotStateReady) != (r.RetiredAt == nil) {
		return fmt.Errorf("%w: retirement timestamp does not match state", ErrRuntimeSnapshotInvalid)
	}
	if r.RetiredAt != nil && r.RetiredAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: retired_at precedes created_at", ErrRuntimeSnapshotInvalid)
	}
	return nil
}

func runtimeSnapshotDigestValid(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func runtimeSnapshotStorageKeyValid(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func cloneRuntimeSnapshotRecord(record RuntimeSnapshotRecord) RuntimeSnapshotRecord {
	if record.RetiredAt != nil {
		retiredAt := *record.RetiredAt
		record.RetiredAt = &retiredAt
	}
	return record
}
