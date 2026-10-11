//go:build linux || darwin

// adr: 794 — capture bytes are read through their original inode authority.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type nativeSnapshotOutputReader interface {
	OpenSnapshotOutput(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
}

// The capture caller must finish snapshot/create while paused before entering
// this boundary. A synchronous consumer may stream bytes to storage, but may
// not retain the descriptor. Reading alone grants no publication or readiness
// receipt and does not enable native snapshot capture.
func (v *JailerVMM) withNativeSnapshotOutput(ctx context.Context, lease Lease, kind string, consume func(*os.File) error) error {
	r := v.nativeRecovery
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if r == nil || r.journal == nil || !ok || consume == nil ||
		permit.Incoming.Execution.InstanceID != lease.Instance || !sameNativePhysicalLease(permit.Incoming.NativeLease, lease) {
		return errors.New("native snapshot output: original capture capability and consumer are required")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[lease.Instance], r.daemonLock
	r.mu.Unlock()
	if generation == "" || generation != permit.Incoming.NativeGeneration || daemonLock == nil {
		return errors.New("native snapshot output: original local producer is required")
	}
	if _, err := daemonLock.Stat(); err != nil {
		return err
	}
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return j.withCaptureOutput(ctx, owner, permit, v.chrootRoot(lease.Instance), kind, consume)
}

// Lock order is incoming capture (held by Manager), physical VM, then source.
// The descriptor closes before either lock releases, including error paths.
func (j *nativeImageSourceJournal) withCaptureOutput(ctx context.Context, expected nativeLaunchRecord, permit nativeSnapshotCapturePermit, root, kind string, consume func(*os.File) error) (err error) {
	backend, ok := j.backend.(nativeSnapshotOutputReader)
	if !ok || consume == nil || !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		filepath.Base(root) != "root" || filepath.Base(filepath.Dir(root)) != expected.Lease.Instance {
		return errors.New("native snapshot output: original jail, reader and consumer are required")
	}
	name, err := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
	if err != nil {
		return err
	}
	vmLock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if err := j.captureOutputAuthority(ctx, expected, owner, permit); err != nil {
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
		return errors.New("native snapshot output: original output epoch changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := backend.OpenSnapshotOutput(locked, lockedRef, j.anchor(locked))
	if file != nil {
		defer func() { err = errors.Join(err, file.Close()) }()
	}
	if err != nil {
		return err
	}
	if file == nil {
		return errors.New("native snapshot output: reader returned no descriptor")
	}
	if err := checkNativeSnapshotOutputDescriptor(file, locked.Identity); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(consume(file), ctx.Err())
}

func (j *nativeImageSourceJournal) captureOutput(owner nativeLaunchRecord, root, name string) (record nativeImageSourceRecord, ref nativeImageReference, err error) {
	records, err := j.records()
	if err != nil {
		return record, ref, err
	}
	found := false
	for _, candidate := range records {
		for _, reference := range candidate.References {
			if !sameNativeImageOwner(reference, owner) || reference.Root != root || reference.Name != name {
				continue
			}
			if found || len(candidate.References) != 1 || candidate.Removed || !candidate.Ready ||
				candidate.Applied != candidate.Desired || !reference.Ready || reference.Removed ||
				reference.TargetRemoved || reference.ReadOnly || reference.Link {
				return record, ref, errors.New("native snapshot output: exclusive original output binding is required")
			}
			record, ref, found = candidate, reference, true
		}
	}
	if !found {
		return record, ref, errors.New("native snapshot output: original output is absent")
	}
	return record, ref, nil
}

func checkNativeSnapshotOutputDescriptor(file *os.File, expected nativeLoopIdentity) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size <= 0 || uint64(st.Dev) != expected.Device || st.Ino != expected.Inode {
		return errors.New("native snapshot output: descriptor differs from original completed output")
	}
	access, accessErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	flags, flagsErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err := errors.Join(accessErr, flagsErr); err != nil {
		return err
	}
	if access&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.FD_CLOEXEC == 0 {
		return errors.New("native snapshot output: descriptor is writable or inheritable")
	}
	// Reject O_PATH without changing the consumer's read offset.
	_, err := file.ReadAt(make([]byte, 1), 0)
	return err
}
