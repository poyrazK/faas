package sched

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CurrentRuntimeSnapshotFormatVersion is part of the immutable catalog key.
// Bumping it makes old Firecracker state ineligible without mutating an
// existing entry or silently restoring an incompatible machine shape.
const CurrentRuntimeSnapshotFormatVersion = 1

var (
	ErrRuntimeSnapshotNotFound    = errors.New("sched: runtime snapshot not found")
	ErrRuntimeSnapshotInvalid     = errors.New("sched: runtime snapshot is invalid")
	ErrRuntimeSnapshotCorrupt     = errors.New("sched: runtime snapshot is corrupt")
	ErrRuntimeSnapshotConflict    = errors.New("sched: runtime snapshot key already exists")
	ErrRuntimeSnapshotUnwired     = errors.New("sched: runtime snapshot catalog is not wired")
	ErrRuntimeSnapshotUnsupported = errors.New("sched: runtime snapshot format is unsupported")
)

// RuntimeSnapshotIdentity is the complete compatibility identity of a
// sanitized runtime machine. Every field participates in the catalog key.
// Tenant source, input, credentials, environment, and mutable app state are
// intentionally absent.
type RuntimeSnapshotIdentity struct {
	Runtime             api.ExecutionRuntime
	Architecture        string
	KernelDigest        string
	GuestExecutorDigest string
	BaseImageDigest     string
	MemoryMB            int
	EphemeralDiskMB     int
	FormatVersion       int
}

// Validate rejects ambiguous identities before they can become lookup keys.
func (i RuntimeSnapshotIdentity) Validate() error {
	if !i.Runtime.Valid() {
		return fmt.Errorf("%w: unsupported runtime %q", ErrRuntimeSnapshotInvalid, i.Runtime)
	}
	if i.Architecture != archAMD64 && i.Architecture != archARM64 {
		return fmt.Errorf("%w: unsupported architecture %q", ErrRuntimeSnapshotInvalid, i.Architecture)
	}
	for _, digest := range []struct {
		name  string
		value string
	}{
		{name: "kernel", value: i.KernelDigest},
		{name: "executor", value: i.GuestExecutorDigest},
		{name: "base", value: i.BaseImageDigest},
	} {
		if !validRuntimeSnapshotDigest(digest.value) {
			return fmt.Errorf("%w: %s digest must be lowercase sha256 hex", ErrRuntimeSnapshotInvalid, digest.name)
		}
	}
	if !api.ValidExecutionMemoryMB(i.MemoryMB) || !api.ValidExecutionEphemeralDiskMB(i.EphemeralDiskMB) {
		return fmt.Errorf("%w: machine shape is outside execution limits", ErrRuntimeSnapshotInvalid)
	}
	if i.FormatVersion <= 0 {
		return fmt.Errorf("%w: format version must be positive", ErrRuntimeSnapshotInvalid)
	}
	return nil
}

// Key returns the canonical object-independent catalog key for the identity.
// StorageKey is kept separate so an object can be relocated without changing
// compatibility semantics.
func (i RuntimeSnapshotIdentity) Key() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"execution-snapshots/v%d/%s/%s/memory-%d/disk-%d/kernel-%s/executor-%s/base-%s",
		i.FormatVersion, i.Runtime, i.Architecture, i.MemoryMB, i.EphemeralDiskMB,
		i.KernelDigest, i.GuestExecutorDigest, i.BaseImageDigest,
	), nil
}

func (i RuntimeSnapshotIdentity) equal(other RuntimeSnapshotIdentity) bool {
	return i == other
}

// RuntimeSnapshot is immutable catalog metadata for one sanitized snapshot.
// A ready entry must prove that it was captured before any tenant payload or
// mutable identity entered the VM.
type RuntimeSnapshot struct {
	Identity   RuntimeSnapshotIdentity
	StorageKey string
	// SnapshotDigest is the SHA-256 of the length-delimited memory and
	// vmstate object pair (see RuntimeSnapshotDigest). A single digest binds
	// both restore inputs, so either object can fail closed independently.
	SnapshotDigest string
	MemBytes       int64
	VMStateBytes   int64
	Sanitized      bool
	PayloadFree    bool
	State          RuntimeSnapshotState
	CreatedAt      time.Time
}

type RuntimeSnapshotState string

const (
	RuntimeSnapshotReady   RuntimeSnapshotState = "ready"
	RuntimeSnapshotRetired RuntimeSnapshotState = "retired"
)

// Validate checks metadata that must hold even when the artifact itself is
// stored outside Postgres. It does not inspect bytes; RuntimeSnapshotVerifier
// supplies that second, digest-backed check.
func (s RuntimeSnapshot) Validate() error {
	if err := s.Identity.Validate(); err != nil {
		return err
	}
	key, err := s.Identity.Key()
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.StorageKey) == "" || !validRuntimeSnapshotStorageKey(s.StorageKey) {
		return fmt.Errorf("%w: storage key is not canonical", ErrRuntimeSnapshotInvalid)
	}
	if !validRuntimeSnapshotDigest(s.SnapshotDigest) {
		return fmt.Errorf("%w: snapshot digest must be lowercase sha256 hex", ErrRuntimeSnapshotInvalid)
	}
	if s.MemBytes <= 0 || s.VMStateBytes <= 0 {
		return fmt.Errorf("%w: snapshot byte sizes must be positive", ErrRuntimeSnapshotInvalid)
	}
	if !s.Sanitized || !s.PayloadFree {
		return fmt.Errorf("%w: snapshot is not proven sanitized and payload-free", ErrRuntimeSnapshotInvalid)
	}
	if s.State != RuntimeSnapshotReady && s.State != RuntimeSnapshotRetired {
		return fmt.Errorf("%w: unsupported state %q", ErrRuntimeSnapshotInvalid, s.State)
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrRuntimeSnapshotInvalid)
	}
	if key == "" {
		return fmt.Errorf("%w: identity key is empty", ErrRuntimeSnapshotInvalid)
	}
	return nil
}

func validRuntimeSnapshotDigest(value string) bool {
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

func validRuntimeSnapshotStorageKey(value string) bool {
	if strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// RuntimeSnapshotRequest contains the platform-pinned inputs needed to
// resolve a snapshot. The caller-controlled portion is the API snapshot shape;
// digests are supplied by the trusted release/runtime installation.
type RuntimeSnapshotRequest struct {
	Shape               api.ExecutionSnapshotShape
	Architecture        string
	KernelDigest        string
	GuestExecutorDigest string
	BaseImageDigest     string
	FormatVersion       int
}

func (r RuntimeSnapshotRequest) Identity() RuntimeSnapshotIdentity {
	return RuntimeSnapshotIdentity{
		Runtime:             r.Shape.Runtime,
		Architecture:        r.Architecture,
		KernelDigest:        r.KernelDigest,
		GuestExecutorDigest: r.GuestExecutorDigest,
		BaseImageDigest:     r.BaseImageDigest,
		MemoryMB:            r.Shape.MemoryMB,
		EphemeralDiskMB:     r.Shape.EphemeralDiskMB,
		FormatVersion:       r.FormatVersion,
	}
}

type RuntimeSnapshotPlanMode string

const (
	RuntimeSnapshotRestore  RuntimeSnapshotPlanMode = "restore_snapshot"
	RuntimeSnapshotColdBoot RuntimeSnapshotPlanMode = "cold_boot"
)

type RuntimeSnapshotFallbackReason string

const (
	RuntimeSnapshotFallbackMissing      RuntimeSnapshotFallbackReason = "missing"
	RuntimeSnapshotFallbackCorrupt      RuntimeSnapshotFallbackReason = "corrupt"
	RuntimeSnapshotFallbackIncompatible RuntimeSnapshotFallbackReason = "incompatible"
	RuntimeSnapshotFallbackRetired      RuntimeSnapshotFallbackReason = "retired"
	RuntimeSnapshotFallbackUnwired      RuntimeSnapshotFallbackReason = "catalog_unwired"
)

// RuntimeSnapshotPlan is the resolver's explicit handoff to vmmd. ColdBoot
// always carries the same sanitized identity, so a cache miss cannot silently
// switch to a tenant or differently shaped image.
type RuntimeSnapshotPlan struct {
	Mode           RuntimeSnapshotPlanMode
	Identity       RuntimeSnapshotIdentity
	Snapshot       *RuntimeSnapshot
	FallbackReason RuntimeSnapshotFallbackReason
}

// RuntimeSnapshotIndex is the durable catalog read seam. Implementations may
// use Postgres, a signed release manifest, or another strongly consistent
// metadata store; the resolver never scans an object bucket for candidates.
type RuntimeSnapshotIndex interface {
	LookupRuntimeSnapshot(ctx context.Context, key string) (RuntimeSnapshot, error)
}

// RuntimeSnapshotVerifier checks the immutable object named by StorageKey and
// SnapshotDigest. ErrRuntimeSnapshotCorrupt means the cache may be bypassed;
// other errors preserve transient storage failures for the caller to retry.
type RuntimeSnapshotVerifier interface {
	VerifyRuntimeSnapshot(ctx context.Context, snapshot RuntimeSnapshot) error
}

// RuntimeSnapshotCatalog resolves one exact identity. A resolver never
// chooses a merely similar snapshot: any mismatch becomes a same-identity
// sanitized cold boot.
type RuntimeSnapshotCatalog struct {
	index         RuntimeSnapshotIndex
	verifier      RuntimeSnapshotVerifier
	formatVersion int
}

func NewRuntimeSnapshotCatalog(index RuntimeSnapshotIndex, verifier RuntimeSnapshotVerifier) *RuntimeSnapshotCatalog {
	return &RuntimeSnapshotCatalog{
		index: index, verifier: verifier, formatVersion: CurrentRuntimeSnapshotFormatVersion,
	}
}

// Resolve returns cold boot for missing, retired, incompatible, or corrupt
// snapshots. Catalog I/O failures are returned so operators do not mistake a
// control-plane outage for a healthy cold-boot fallback.
func (c *RuntimeSnapshotCatalog) Resolve(ctx context.Context, request RuntimeSnapshotRequest) (RuntimeSnapshotPlan, error) {
	identity := request.Identity()
	if err := identity.Validate(); err != nil {
		return RuntimeSnapshotPlan{}, err
	}
	if c == nil {
		return RuntimeSnapshotPlan{Mode: RuntimeSnapshotColdBoot, Identity: identity, FallbackReason: RuntimeSnapshotFallbackUnwired}, nil
	}
	if identity.FormatVersion != c.formatVersion {
		return RuntimeSnapshotPlan{}, fmt.Errorf("%w: got %d, want %d", ErrRuntimeSnapshotUnsupported, identity.FormatVersion, c.formatVersion)
	}
	plan := RuntimeSnapshotPlan{Mode: RuntimeSnapshotColdBoot, Identity: identity}
	if c.index == nil {
		plan.FallbackReason = RuntimeSnapshotFallbackUnwired
		return plan, nil
	}
	key, err := identity.Key()
	if err != nil {
		return RuntimeSnapshotPlan{}, err
	}
	entry, err := c.index.LookupRuntimeSnapshot(ctx, key)
	if errors.Is(err, ErrRuntimeSnapshotNotFound) {
		plan.FallbackReason = RuntimeSnapshotFallbackMissing
		return plan, nil
	}
	if errors.Is(err, ErrRuntimeSnapshotCorrupt) {
		plan.FallbackReason = RuntimeSnapshotFallbackCorrupt
		return plan, nil
	}
	if err != nil {
		return RuntimeSnapshotPlan{}, fmt.Errorf("sched: lookup runtime snapshot: %w", err)
	}
	if err := entry.Validate(); err != nil {
		plan.FallbackReason = RuntimeSnapshotFallbackCorrupt
		return plan, nil //nolint:nilerr // invalid cache metadata is an explicit cold-boot fallback.
	}
	if !entry.Identity.equal(identity) {
		plan.FallbackReason = RuntimeSnapshotFallbackIncompatible
		return plan, nil
	}
	if entry.State == RuntimeSnapshotRetired {
		plan.FallbackReason = RuntimeSnapshotFallbackRetired
		return plan, nil
	}
	if c.verifier == nil {
		plan.FallbackReason = RuntimeSnapshotFallbackUnwired
		return plan, nil
	}
	if err := c.verifier.VerifyRuntimeSnapshot(ctx, entry); err != nil {
		if errors.Is(err, ErrRuntimeSnapshotCorrupt) {
			plan.FallbackReason = RuntimeSnapshotFallbackCorrupt
			return plan, nil
		}
		if errors.Is(err, ErrRuntimeSnapshotUnwired) {
			plan.FallbackReason = RuntimeSnapshotFallbackUnwired
			return plan, nil
		}
		return RuntimeSnapshotPlan{}, fmt.Errorf("sched: verify runtime snapshot: %w", err)
	}
	plan.Mode = RuntimeSnapshotRestore
	plan.Snapshot = &entry
	return plan, nil
}

// MemoryRuntimeSnapshotIndex is a concurrency-safe immutable test/local index.
// Production implementations should back the same interface with a durable
// catalog and signed publication path.
type MemoryRuntimeSnapshotIndex struct {
	mu      sync.RWMutex
	entries map[string]RuntimeSnapshot
}

func NewMemoryRuntimeSnapshotIndex() *MemoryRuntimeSnapshotIndex {
	return &MemoryRuntimeSnapshotIndex{entries: make(map[string]RuntimeSnapshot)}
}

// Publish validates and inserts an entry once. Replacing a key is forbidden,
// even with a different storage location, because key immutability is the
// compatibility fence for restored Firecracker state.
func (m *MemoryRuntimeSnapshotIndex) Publish(entry RuntimeSnapshot) error {
	if m == nil {
		return ErrRuntimeSnapshotUnwired
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.State != RuntimeSnapshotReady {
		return fmt.Errorf("%w: only ready entries may be published", ErrRuntimeSnapshotInvalid)
	}
	key, err := entry.Identity.Key()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[key]; exists {
		return ErrRuntimeSnapshotConflict
	}
	m.entries[key] = entry
	return nil
}

func (m *MemoryRuntimeSnapshotIndex) LookupRuntimeSnapshot(_ context.Context, key string) (RuntimeSnapshot, error) {
	if m == nil {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotUnwired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[key]
	if !ok {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotNotFound
	}
	return entry, nil
}
