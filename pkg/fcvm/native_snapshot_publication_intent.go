// adr: 568 — persistent intent never grants recovered capture or cleanup authority.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Configure before native recovery. This enables only durable intent storage;
// qualification capture, restore and artifact cleanup remain gated.
func (v *JailerVMM) WithNativeSnapshotPublicationRoot(directory string) *JailerVMM {
	v.nativeSnapshotPublicationRoot = directory
	return v
}

type nativeSnapshotPublicationObjectKeys struct {
	Memory  string `json:"memory"`
	VMState string `json:"vm_state"`
	Drive   string `json:"drive"`
	Backing string `json:"backing"`
}

func nativeSnapshotIntentKeys(incoming nativeQualificationRecord) nativeSnapshotPublicationObjectKeys {
	keys := qualificationSnapshotProof(incoming, SnapshotInfo{})
	return nativeSnapshotPublicationObjectKeys{Memory: keys.StorageKey, VMState: keys.VMStateStorageKey, Drive: keys.DriveStorageKey, Backing: keys.BackingStorageKey}
}

type nativeSnapshotPublicationIntent struct {
	Version   int                                 `json:"version"`
	Directory nativeLoopIdentity                  `json:"directory"`
	File      nativeLoopIdentity                  `json:"file"`
	JailBase  string                              `json:"jail_base"`
	Incoming  nativeQualificationRecord           `json:"incoming"`
	Capture   nativeQualificationCaptureRecord    `json:"capture"`
	Physical  nativeLaunchRecord                  `json:"physical"`
	Keys      nativeSnapshotPublicationObjectKeys `json:"keys"`
}

func (r *nativeSnapshotPublicationIntent) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"version", "directory", "file", "jail_base", "incoming", "capture", "physical", "keys"})
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["directory"], []string{"device", "inode"}); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["file"], []string{"device", "inode"}); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["keys"], []string{"memory", "vm_state", "drive", "backing"}); err != nil {
		return err
	}
	type plain nativeSnapshotPublicationIntent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func (r nativeSnapshotPublicationIntent) validate() error {
	if err := errors.Join(r.Incoming.validate(r.Incoming.Execution.NodeID), r.Capture.validate(r.Incoming), r.Physical.validate(r.Incoming.Execution.InstanceID)); err != nil {
		return err
	}
	if r.Version != 1 || r.Directory.Device == 0 || r.Directory.Inode == 0 || r.File.Device != r.Directory.Device || r.File.Inode == 0 || r.File.Inode == r.Directory.Inode || !filepath.IsAbs(r.JailBase) || filepath.Clean(r.JailBase) != r.JailBase || r.JailBase == "/" ||
		!r.Incoming.CreateStarted || r.Incoming.Revoked || !r.Capture.CompletedAt.IsZero() || !liveNativeSnapshotOwner(r.Physical) ||
		r.Physical.Generation != r.Incoming.NativeGeneration || r.Physical.KernelBootID != r.Incoming.KernelBootID || !sameNativePhysicalLease(r.Physical.Lease, r.Incoming.NativeLease) ||
		r.Keys != nativeSnapshotIntentKeys(r.Incoming) {
		return errors.New("native snapshot publication: persistent intent differs from its original capture")
	}
	return nil
}

type nativeSnapshotPublicationJournal interface {
	Acquire(context.Context) error
	Check() error
	Begin(context.Context, nativeSnapshotPublicationIntent) (nativeSnapshotPublicationIntent, error)
	Require(context.Context, nativeSnapshotPublicationIntent) error
}

type nativeSnapshotPublicationContextKey struct{}

type nativeSnapshotPublicationPermit struct {
	journal nativeSnapshotPublicationJournal
	intent  nativeSnapshotPublicationIntent
	backend storage.StorageBackend // original canonical backend; never reconstructed from disk
}

// The caller retains the incoming lock through the complete capture. Only this
// original successful call creates publication authority; inventory cannot.
func (v *JailerVMM) beginNativeSnapshotPublication(ctx context.Context, lease Lease) (context.Context, error) {
	r := v.nativeRecovery
	if r == nil || r.publications == nil {
		return ctx, errors.New("native snapshot publication: persistent intent adapter is unavailable")
	}
	backend := v.storage
	if _, ok := r.publications.(nativeSnapshotPublicationReceiptJournal); !ok {
		return ctx, errors.New("native snapshot publication: original receipt journal is unavailable")
	}
	if _, err := v.preflightNativeSnapshotPublicationTo(ctx, lease, backend); err != nil {
		return ctx, err
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return ctx, err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return ctx, err
	}
	owner, err := r.journal.read(lease.Instance)
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	if err == nil {
		err = images.captureOutputAuthority(ctx, permit.Physical, owner, permit)
	}
	intent := nativeSnapshotPublicationIntent{Version: 1, JailBase: v.chrootBase, Incoming: permit.Incoming, Capture: permit.Capture, Physical: permit.Physical, Keys: nativeSnapshotIntentKeys(permit.Incoming)}
	intent.Incoming.NativeLease.processGeneration, intent.Physical.Lease.processGeneration = 0, 0
	if err == nil {
		intent, err = r.publications.Begin(ctx, intent)
	}
	if err == nil {
		err = errors.Join(r.checkDaemonOwnership(), images.captureOutputAuthority(ctx, permit.Physical, owner, permit))
		if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
			err = errors.Join(err, state.ErrConflict)
		}
	}
	if err := errors.Join(err, lock.Close(), ctx.Err()); err != nil {
		return ctx, err // An uncertain durable write has no live publication permit.
	}
	return context.WithValue(ctx, nativeSnapshotPublicationContextKey{}, nativeSnapshotPublicationPermit{journal: r.publications, intent: intent, backend: backend}), nil
}

func (v *JailerVMM) requireNativeSnapshotPublication(ctx context.Context, lease Lease) (nativeSnapshotPublicationPermit, error) {
	p, ok := ctx.Value(nativeSnapshotPublicationContextKey{}).(nativeSnapshotPublicationPermit)
	if !ok || v.nativeRecovery == nil || p.journal == nil || p.journal != v.nativeRecovery.publications || p.backend == nil {
		return p, errors.New("native snapshot publication: original persistent begin capability is required")
	}
	capture, _, err := nativeSnapshotPublicationKeys(ctx, lease)
	if err != nil {
		return p, err
	}
	capture.Incoming.NativeLease.processGeneration = 0
	if p.intent.Capture != capture.Capture || p.intent.Incoming != capture.Incoming || !sameNativeSnapshotProcess(p.intent.Physical, capture.Physical) {
		return p, state.ErrConflict
	}
	return p, p.journal.Require(ctx, p.intent)
}
