package sched

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// RuntimeSnapshotPublication describes one trusted, tenant-free capture.
// Memory and VMState are streamed directly to the storage backend; callers
// must set both trust markers only after the guest has been scrubbed and the
// payload-free boundary has been proven.
type RuntimeSnapshotPublication struct {
	Identity     RuntimeSnapshotIdentity
	Memory       io.Reader
	VMState      io.Reader
	MemBytes     int64
	VMStateBytes int64
	CreatedAt    time.Time
	Sanitized    bool
	PayloadFree  bool
}

// RuntimeSnapshotPublisher is the trusted release/publication boundary for
// disposable execution snapshots. Storage is written before the durable
// catalog row, so a ready row can never point at a partially written pair.
type RuntimeSnapshotPublisher struct {
	store   state.RuntimeSnapshotStore
	storage storage.StorageBackend
	now     func() time.Time
}

// Runtime snapshots use the storage backend's existing snapshot namespace so
// local and OCI drivers apply the same atomic pair semantics. The nil UUID is
// reserved for platform-owned runtime captures; the durable catalog remains
// the authority that binds each generated capture UUID to its identity.
const runtimeSnapshotStorageNamespace = "00000000-0000-0000-0000-000000000000"

// NewRuntimeSnapshotPublisher wires the durable catalog and artifact store.
// A nil dependency is treated as unwired by PublishRuntimeSnapshot.
func NewRuntimeSnapshotPublisher(store state.RuntimeSnapshotStore, backend storage.StorageBackend) *RuntimeSnapshotPublisher {
	return &RuntimeSnapshotPublisher{store: store, storage: backend, now: time.Now}
}

// PublishRuntimeSnapshot streams and authenticates a sanitized capture, then
// inserts its immutable catalog record. Each attempt receives a fresh storage
// key, which makes cleanup on failed publication safe and keeps catalog rows
// insert-only even when a release retry races with an earlier attempt.
func (p *RuntimeSnapshotPublisher) PublishRuntimeSnapshot(ctx context.Context, publication RuntimeSnapshotPublication) (RuntimeSnapshot, error) {
	if p == nil || p.store == nil || p.storage == nil {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotUnwired
	}
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if err := publication.Identity.Validate(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if publication.Memory == nil || publication.VMState == nil {
		return RuntimeSnapshot{}, fmt.Errorf("%w: capture readers are required", ErrRuntimeSnapshotInvalid)
	}
	if publication.MemBytes <= 0 || publication.VMStateBytes <= 0 {
		return RuntimeSnapshot{}, fmt.Errorf("%w: snapshot byte sizes must be positive", ErrRuntimeSnapshotInvalid)
	}
	if !publication.Sanitized || !publication.PayloadFree {
		return RuntimeSnapshot{}, fmt.Errorf("%w: capture must be sanitized and payload-free", ErrRuntimeSnapshotInvalid)
	}
	createdAt := publication.CreatedAt
	if createdAt.IsZero() {
		createdAt = p.now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}
	if createdAt.IsZero() {
		return RuntimeSnapshot{}, fmt.Errorf("%w: created_at is required", ErrRuntimeSnapshotInvalid)
	}

	catalogKey, err := publication.Identity.Key()
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if err := p.ensureCatalogKeyAvailable(ctx, catalogKey); err != nil {
		return RuntimeSnapshot{}, err
	}

	storageKey := runtimeSnapshotPublicationStorageKey()
	vmstateKey := runtimeSnapshotVMStateStorageKey(storageKey)
	if err := p.ensureObjectAbsent(ctx, storageKey); err != nil {
		return RuntimeSnapshot{}, err
	}
	if err := p.ensureObjectAbsent(ctx, vmstateKey); err != nil {
		return RuntimeSnapshot{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			p.cleanupObjects(cleanupCtx, storageKey, vmstateKey)
		}
	}()

	h := sha256.New()
	_, _ = io.WriteString(h, runtimeSnapshotDigestDomain)
	memoryReader := newRuntimeSnapshotPublicationReader(publication.Memory, h, "mem", publication.MemBytes)
	if err := p.storage.Put(ctx, storageKey, memoryReader); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("sched: publish runtime snapshot memory: %w", err)
	}
	if err := memoryReader.complete(); err != nil {
		return RuntimeSnapshot{}, err
	}

	vmstateReader := newRuntimeSnapshotPublicationReader(publication.VMState, h, "vmstate", publication.VMStateBytes)
	if err := p.storage.Put(ctx, vmstateKey, vmstateReader); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("sched: publish runtime snapshot vmstate: %w", err)
	}
	if err := vmstateReader.complete(); err != nil {
		return RuntimeSnapshot{}, err
	}

	entry := RuntimeSnapshot{
		Identity:       publication.Identity,
		StorageKey:     storageKey,
		SnapshotDigest: hex.EncodeToString(h.Sum(nil)),
		MemBytes:       publication.MemBytes,
		VMStateBytes:   publication.VMStateBytes,
		Sanitized:      true,
		PayloadFree:    true,
		State:          RuntimeSnapshotReady,
		CreatedAt:      createdAt,
	}
	if err := entry.Validate(); err != nil {
		return RuntimeSnapshot{}, err
	}
	if err := NewStorageRuntimeSnapshotVerifier(p.storage).VerifyRuntimeSnapshot(ctx, entry); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("sched: verify published runtime snapshot: %w", err)
	}

	record := state.RuntimeSnapshotRecord{
		CatalogKey:          catalogKey,
		Runtime:             entry.Identity.Runtime,
		Architecture:        entry.Identity.Architecture,
		KernelDigest:        entry.Identity.KernelDigest,
		GuestExecutorDigest: entry.Identity.GuestExecutorDigest,
		BaseImageDigest:     entry.Identity.BaseImageDigest,
		MemoryMB:            entry.Identity.MemoryMB,
		EphemeralDiskMB:     entry.Identity.EphemeralDiskMB,
		FormatVersion:       entry.Identity.FormatVersion,
		StorageKey:          entry.StorageKey,
		SnapshotDigest:      entry.SnapshotDigest,
		MemBytes:            entry.MemBytes,
		VMStateBytes:        entry.VMStateBytes,
		Sanitized:           entry.Sanitized,
		PayloadFree:         entry.PayloadFree,
		State:               string(entry.State),
		CreatedAt:           entry.CreatedAt,
	}
	stored, err := p.store.PublishRuntimeSnapshot(ctx, record)
	if errors.Is(err, state.ErrConflict) {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotConflict
	}
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("sched: publish runtime snapshot catalog: %w", err)
	}
	entry.CreatedAt = stored.CreatedAt
	cleanup = false
	return entry, nil
}

func (p *RuntimeSnapshotPublisher) ensureCatalogKeyAvailable(ctx context.Context, key string) error {
	_, err := p.store.LookupRuntimeSnapshot(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err == nil {
		return ErrRuntimeSnapshotConflict
	}
	if errors.Is(err, state.ErrRuntimeSnapshotInvalid) {
		return fmt.Errorf("%w: catalog key contains an invalid existing row", ErrRuntimeSnapshotConflict)
	}
	return fmt.Errorf("sched: check runtime snapshot catalog: %w", err)
}

func (p *RuntimeSnapshotPublisher) ensureObjectAbsent(ctx context.Context, key string) error {
	reader, err := p.storage.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sched: check runtime snapshot object %q: %w", key, err)
	}
	if reader != nil {
		_ = reader.Close()
	}
	return ErrRuntimeSnapshotConflict
}

func (p *RuntimeSnapshotPublisher) cleanupObjects(ctx context.Context, keys ...string) {
	for _, key := range keys {
		if err := p.storage.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrDeleteUnsupported) {
			// Cleanup is best effort. The catalog row is never inserted on this
			// path, so an orphan is inert and can be reclaimed by storage GC.
			continue
		}
	}
}

func runtimeSnapshotPublicationStorageKey() string {
	return fmt.Sprintf("snap/%s/captures/%s/mem", runtimeSnapshotStorageNamespace, uuid.NewString())
}

type runtimeSnapshotPublicationReader struct {
	source      io.Reader
	hash        io.Writer
	name        string
	expected    int64
	read        int64
	sourceEOF   bool
	emptyReads  int
	initialized bool
}

func newRuntimeSnapshotPublicationReader(source io.Reader, hash io.Writer, name string, expected int64) *runtimeSnapshotPublicationReader {
	return &runtimeSnapshotPublicationReader{source: source, hash: hash, name: name, expected: expected}
}

func (r *runtimeSnapshotPublicationReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if !r.initialized {
		writeRuntimeSnapshotDigestHeader(r.hash, r.name, r.expected)
		r.initialized = true
	}
	if r.sourceEOF {
		return 0, io.EOF
	}
	if r.read >= r.expected {
		var one [1]byte
		n, err := r.source.Read(one[:])
		if n > 0 {
			return 0, fmt.Errorf("%w: %s capture is oversized", ErrRuntimeSnapshotInvalid, r.name)
		}
		if errors.Is(err, io.EOF) {
			r.sourceEOF = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.emptyReads++
		if r.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
		return 0, nil
	}
	remaining := r.expected - r.read
	if int64(len(dst)) > remaining {
		dst = dst[:remaining]
	}
	n, err := r.source.Read(dst)
	if n > 0 {
		r.emptyReads = 0
		if _, hashErr := r.hash.Write(dst[:n]); hashErr != nil {
			return n, hashErr
		}
		r.read += int64(n)
	}
	if errors.Is(err, io.EOF) {
		r.sourceEOF = true
	}
	if n == 0 && err == nil {
		r.emptyReads++
		if r.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
	}
	return n, err
}

func (r *runtimeSnapshotPublicationReader) complete() error {
	if r.read != r.expected || !r.sourceEOF {
		return fmt.Errorf("%w: %s capture size is %d, want %d", ErrRuntimeSnapshotInvalid, r.name, r.read, r.expected)
	}
	return nil
}

func writeRuntimeSnapshotDigestHeader(w io.Writer, name string, size int64) {
	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, "\x00")
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(size))
	_, _ = w.Write(encoded[:])
}
