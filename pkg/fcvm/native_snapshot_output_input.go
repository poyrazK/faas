// adr: 568 — capture publication reads only original owned output inodes.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

type nativeSnapshotOutputInputBackend interface {
	OpenSnapshotOutput(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
}

// The consumer finishes synchronously while the original VM and output epoch
// remain locked. Descriptors and their procfs names cannot escape this boundary.
// This grants read authority only; capture completion still requires the
// separate pause, frozen drive, publication and durable completion protocol.
func (v *JailerVMM) withNativeSnapshotOutput(ctx context.Context, lease Lease, kind string, consume func(*os.File) error) error {
	r := v.nativeRecovery
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if r == nil || r.journal == nil || !ok || consume == nil || permit.Incoming.Execution.InstanceID != lease.Instance || !sameNativePhysicalLease(permit.Incoming.NativeLease, lease) {
		return errors.New("native snapshot output input: original capture capability is required")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[lease.Instance], r.daemonLock
	r.mu.Unlock()
	if generation == "" || generation != permit.Incoming.NativeGeneration || daemonLock == nil {
		return errors.New("native snapshot output input: original daemon producer is required")
	}
	if _, err := daemonLock.Stat(); err != nil {
		return err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return j.withCaptureOutput(ctx, permit, v.chrootRoot(lease.Instance), kind, consume)
}

func (j *nativeImageSourceJournal) withCaptureOutput(ctx context.Context, permit nativeSnapshotCapturePermit, root, kind string, consume func(*os.File) error) (err error) {
	backend, ok := j.backend.(nativeSnapshotOutputInputBackend)
	if !ok || consume == nil {
		return errors.New("native snapshot output input: pinned output backend or consumer is unavailable")
	}
	name, err := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
	if err != nil {
		return err
	}
	vmLock, err := j.owner.lock(ctx, permit.Physical.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	owner, err := j.owner.read(permit.Physical.Lease.Instance)
	if err != nil {
		return err
	}
	if err := j.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return err
	}
	record, ref, err := j.captureOutput(owner, root, name)
	if err != nil {
		return err
	}
	sourceLock, err := j.lock(ctx, record.Identity)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sourceLock.Close()) }()
	locked, lockedRef, err := j.captureOutput(owner, root, name)
	if err != nil {
		return err
	}
	if locked.Epoch != record.Epoch || locked.Identity != record.Identity || locked.Namespace != record.Namespace || lockedRef != ref {
		return errors.New("native snapshot output input: original output epoch changed")
	}
	if err := j.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return err
	}
	file, err := backend.OpenSnapshotOutput(locked, lockedRef, j.anchor(locked))
	if err != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return err
	}
	if file == nil {
		return errors.New("native snapshot output input: backend returned no pinned output")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	identity, err := resourceFileID(info)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || identity.Device != locked.Identity.Device || identity.Inode != locked.Identity.Inode {
		return errors.Join(err, errors.New("native snapshot output input: original output is absent or incomplete"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(consume(file), ctx.Err())
}

func (j *nativeImageSourceJournal) captureOutput(owner nativeLaunchRecord, root, name string) (record nativeImageSourceRecord, ref nativeImageReference, err error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != "root" || filepath.Base(filepath.Dir(root)) != owner.Lease.Instance {
		return record, ref, errors.New("native snapshot output input: original jail root is required")
	}
	records, err := j.records()
	if err != nil {
		return record, ref, err
	}
	found := false
	for _, candidate := range records {
		for _, reference := range candidate.References {
			if !sameNativeImageOwner(reference, owner) || reference.Root != root || reference.Name != name || reference.Removed {
				continue
			}
			if found || len(candidate.References) != 1 {
				return record, ref, errors.New("native snapshot output input: output has ambiguous ownership")
			}
			if candidate.Removed || !candidate.Ready || candidate.Applied != candidate.Desired || !reference.Ready || reference.TargetRemoved || reference.ReadOnly || reference.Link {
				return record, ref, errors.New("native snapshot output input: original output binding is incomplete")
			}
			record, ref, found = candidate, reference, true
		}
	}
	if !found {
		return record, ref, errors.New("native snapshot output input: original output is absent")
	}
	return record, ref, nil
}
