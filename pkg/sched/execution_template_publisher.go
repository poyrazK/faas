package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ExecutionTemplateSpec comes only from the operator's release installation.
// It has no execution ID, tenant payload, environment or outbound capability.
type ExecutionTemplateSpec struct {
	AccountID, NodeID string
	Identity          RuntimeSnapshotIdentity
	Artifacts         ExecutionRuntimeArtifacts
}

type ExecutionTemplateCapture struct {
	NodeID       string
	Snapshot     SnapshotRef
	VMStateBytes int64
}

type ExecutionTemplateCapturer interface {
	CaptureExecutionTemplate(context.Context, ExecutionTemplateSpec) (ExecutionTemplateCapture, error)
	RetireExecutionTemplate(context.Context, ExecutionTemplateCapture) error
}

// ExecutionTemplatePublisher drives the production producer. The ready row is
// the commit point: complete native objects are checked first and stay in
// their original namespace, preserving the frozen drive and backing sidecar.
type ExecutionTemplatePublisher struct {
	store    state.RuntimeSnapshotStore
	backend  storage.StorageBackend
	capturer ExecutionTemplateCapturer
}

func NewExecutionTemplatePublisher(store state.RuntimeSnapshotStore, backend storage.StorageBackend, capturer ExecutionTemplateCapturer) *ExecutionTemplatePublisher {
	return &ExecutionTemplatePublisher{store: store, backend: backend, capturer: capturer}
}

// Ensure is idempotent for a verified ready identity. A retirement is never
// overwritten. A failed/partial capture cannot create a ready row. An uncertain
// database acknowledgement retains its own objects, because COMMIT may have
// succeeded; a retry discovers and verifies that same durable winner.
func (p *ExecutionTemplatePublisher) Ensure(ctx context.Context, spec ExecutionTemplateSpec) (RuntimeSnapshot, error) {
	if p == nil || p.store == nil || p.backend == nil || p.capturer == nil {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotUnwired
	}
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	shape := api.ExecutionSnapshotShape{Profile: spec.Identity.Profile, Runtime: spec.Identity.Runtime, MemoryMB: spec.Identity.MemoryMB, EphemeralDiskMB: spec.Identity.EphemeralDiskMB}
	if uuid.Validate(spec.AccountID) != nil || spec.NodeID == "" || spec.Identity.FormatVersion != 2 || !strings.HasPrefix(spec.Artifacts.BaseKey, "base/") || !strings.HasPrefix(spec.Artifacts.LayerKey, "layers/run-") {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotInvalid
	}
	if err := spec.Artifacts.validate(spec.Identity.Runtime, shape); err != nil {
		return RuntimeSnapshot{}, err
	}
	want := RuntimeSnapshotRequest{FCVersion: spec.Artifacts.FCVersion, Shape: shape, Architecture: spec.Artifacts.Architecture, KernelDigest: spec.Artifacts.KernelDigest, GuestExecutorDigest: spec.Artifacts.GuestExecutorDigest, BaseImageDigest: spec.Artifacts.BaseImageDigest, FormatVersion: 2}.Identity()
	if !want.equal(spec.Identity) {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotInvalid
	}
	key, err := spec.Identity.Key()
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if entry, err := p.existing(ctx, key); !errors.Is(err, ErrRuntimeSnapshotNotFound) {
		return entry, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.ExecutionTemplateCaptureTimeout)
	defer cancel()
	capture, err := p.capturer.CaptureExecutionTemplate(ctx, spec)
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("sched: capture execution template: %w", err)
	}
	// Never allow a malformed node reply to confer deletion authority over
	// another namespace. The production adapter checks this before returning.
	if !executionTemplateStorageKey(capture.Snapshot.StorageKey) || capture.NodeID != spec.NodeID || capture.Snapshot.VMStateStorageKey != runtimeSnapshotVMStateStorageKey(capture.Snapshot.StorageKey) {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotInvalid
	}
	retire := func() error {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		return p.capturer.RetireExecutionTemplate(cleanup, capture)
	}
	entry := RuntimeSnapshot{Identity: spec.Identity, StorageKey: capture.Snapshot.StorageKey, MemBytes: capture.Snapshot.MemBytes, VMStateBytes: capture.VMStateBytes, Sanitized: true, PayloadFree: true, State: RuntimeSnapshotReady, CreatedAt: time.Now().UTC()}
	if !capture.Snapshot.Networkless || capture.Snapshot.FCVersion != spec.Artifacts.FCVersion {
		return RuntimeSnapshot{}, errors.Join(ErrRuntimeSnapshotInvalid, retire())
	}
	entry.SnapshotDigest, err = RuntimeSnapshotBundleDigest(ctx, p.backend, entry)
	if err != nil {
		return RuntimeSnapshot{}, errors.Join(err, retire())
	}
	if err := entry.Validate(); err != nil {
		return RuntimeSnapshot{}, errors.Join(err, retire())
	}
	stored, publishErr := p.store.PublishRuntimeSnapshot(ctx, runtimeSnapshotRecord(entry))
	if publishErr == nil {
		entry.CreatedAt = stored.CreatedAt
		return entry, nil
	}
	// Read with a fresh bounded context after a lost acknowledgement or CAS
	// race. Only a confirmed *different* winner permits deleting this capture.
	lookup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	winner, lookupErr := p.existing(lookup, key)
	if lookupErr == nil {
		if winner.StorageKey != entry.StorageKey {
			if err := retire(); err != nil {
				return RuntimeSnapshot{}, err
			}
		}
		return winner, nil
	}
	return RuntimeSnapshot{}, fmt.Errorf("sched: publication acknowledgement uncertain; capture retained: %w", errors.Join(publishErr, lookupErr))
}

func (p *ExecutionTemplatePublisher) existing(ctx context.Context, key string) (RuntimeSnapshot, error) {
	entry, err := NewStateRuntimeSnapshotIndex(p.store).LookupRuntimeSnapshot(ctx, key)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	if entry.State != RuntimeSnapshotReady {
		return RuntimeSnapshot{}, ErrRuntimeSnapshotConflict
	}
	if err := NewStorageRuntimeSnapshotVerifier(p.backend).VerifyRuntimeSnapshot(ctx, entry); err != nil {
		return RuntimeSnapshot{}, err
	}
	return entry, nil
}

func runtimeSnapshotRecord(entry RuntimeSnapshot) state.RuntimeSnapshotRecord {
	i := entry.Identity
	key, _ := i.Key()
	return state.RuntimeSnapshotRecord{FCVersion: i.FCVersion, Profile: i.Profile, CatalogKey: key, Runtime: i.Runtime, Architecture: i.Architecture, KernelDigest: i.KernelDigest, GuestExecutorDigest: i.GuestExecutorDigest, BaseImageDigest: i.BaseImageDigest, MemoryMB: i.MemoryMB, EphemeralDiskMB: i.EphemeralDiskMB, FormatVersion: i.FormatVersion, StorageKey: entry.StorageKey, SnapshotDigest: entry.SnapshotDigest, MemBytes: entry.MemBytes, VMStateBytes: entry.VMStateBytes, Sanitized: entry.Sanitized, PayloadFree: entry.PayloadFree, State: string(entry.State), CreatedAt: entry.CreatedAt}
}
