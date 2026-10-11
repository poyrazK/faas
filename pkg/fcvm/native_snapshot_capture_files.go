//go:build linux || darwin

// adr: 795 — one capture retains all original source epochs through publication.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

type nativeSnapshotCaptureFiles struct {
	memory, vmstate, drive *os.File
	check                  func(context.Context) error
}

type nativeSnapshotCaptureFileBackend interface {
	nativeSnapshotInputBackend
	nativeSnapshotOutputReader
	nativeSnapshotFrozenDriveBackend
}

// The caller must finish snapshot/create while the original VM is paused.
// Unlike nesting the single-file readers, this takes the physical lock once.
// No descriptor may escape the synchronous consumer; freezing precedes it.
func (v *JailerVMM) withNativeSnapshotCaptureFiles(ctx context.Context, lease Lease, directory string, consume func(nativeSnapshotCaptureFiles) error) error {
	r := v.nativeRecovery
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if r == nil || r.journal == nil || !ok || consume == nil ||
		!sameNativePhysicalLease(permit.Incoming.NativeLease, lease) ||
		!filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return errors.New("native snapshot files: original capture, disk directory and consumer are required")
	}
	checkLocal := func(ctx context.Context) error {
		r.mu.Lock()
		generation, daemonLock := r.owned[lease.Instance], r.daemonLock
		r.mu.Unlock()
		if generation == "" || generation != permit.Incoming.NativeGeneration || daemonLock == nil {
			return errors.New("native snapshot files: original local producer is required")
		}
		_, err := daemonLock.Stat()
		return errors.Join(err, ctx.Err())
	}
	if err := checkLocal(ctx); err != nil {
		return err
	}
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return j.withCaptureFiles(ctx, owner, permit, v.chrootRoot(lease.Instance), directory, checkLocal, consume)
}

type nativeSnapshotCaptureSource struct {
	record nativeImageSourceRecord
	ref    nativeImageReference
}

func (j *nativeImageSourceJournal) captureSources(owner nativeLaunchRecord, permit nativeSnapshotCapturePermit, root string) (sources [3]nativeSnapshotCaptureSource, err error) {
	for i, kind := range []string{"mem", "vmstate"} {
		name, nameErr := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
		if nameErr != nil {
			return sources, nameErr
		}
		sources[i].record, sources[i].ref, err = j.captureOutput(owner, root, name)
		if err != nil {
			return sources, err
		}
	}
	sources[2].record, sources[2].ref, err = j.snapshotDrive(owner, root)
	if err != nil {
		return sources, err
	}
	for i, source := range sources {
		if len(source.record.References) != 1 {
			return sources, errors.New("native snapshot files: each source must be exclusive to the original VM")
		}
		for _, prior := range sources[:i] {
			if source.record.Identity == prior.record.Identity {
				return sources, errors.New("native snapshot files: capture parts alias one source inode")
			}
		}
	}
	return sources, nil
}

// Lock order is incoming capture (Manager), physical VM, then source identities
// in canonical order. All files close before any source or physical lock.
func (j *nativeImageSourceJournal) withCaptureFiles(ctx context.Context, expected nativeLaunchRecord, permit nativeSnapshotCapturePermit, root, directory string, checkLocal func(context.Context) error, consume func(nativeSnapshotCaptureFiles) error) (err error) {
	backend, ok := j.backend.(nativeSnapshotCaptureFileBackend)
	if !ok || !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != "root" || filepath.Base(filepath.Dir(root)) != expected.Lease.Instance {
		return errors.New("native snapshot files: original jail and complete descriptor backend are required")
	}
	vmLock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	check := func(ctx context.Context) error {
		owner, err := j.owner.read(expected.Lease.Instance)
		if err != nil {
			return err
		}
		return errors.Join(checkLocal(ctx), j.captureOutputAuthority(ctx, expected, owner, permit))
	}
	if err := check(ctx); err != nil {
		return err
	}
	sources, err := j.captureSources(expected, permit, root)
	if err != nil {
		return err
	}
	order := []int{0, 1, 2}
	sort.Slice(order, func(a, b int) bool {
		return nativeImageSourceKey(sources[order[a]].record.Identity) < nativeImageSourceKey(sources[order[b]].record.Identity)
	})
	for _, i := range order {
		lock, lockErr := j.lock(ctx, sources[i].record.Identity)
		if lockErr != nil {
			return lockErr
		}
		defer func() { err = errors.Join(err, lock.Close()) }()
	}
	locked, err := j.captureSources(expected, permit, root)
	if err != nil || !reflect.DeepEqual(locked, sources) {
		return errors.Join(err, errors.New("native snapshot files: original source epoch changed"))
	}
	if err := check(ctx); err != nil {
		return err
	}
	files, closeFiles, err := j.openCaptureFiles(ctx, backend, locked, directory)
	defer func() { err = errors.Join(err, closeFiles()) }()
	if err != nil {
		return err
	}
	files.check = check
	return errors.Join(consume(files), check(ctx))
}

func (j *nativeImageSourceJournal) openCaptureFiles(ctx context.Context, backend nativeSnapshotCaptureFileBackend, sources [3]nativeSnapshotCaptureSource, directory string) (files nativeSnapshotCaptureFiles, closeFiles func() error, err error) {
	var opened []*os.File
	closeFiles = func() (err error) {
		for i := len(opened) - 1; i >= 0; i-- {
			err = errors.Join(err, opened[i].Close())
		}
		return err
	}
	var descriptors [3]*os.File
	for i, source := range sources {
		if err := ctx.Err(); err != nil {
			return files, closeFiles, err
		}
		var file *os.File
		if i == 2 {
			file, err = backend.OpenSnapshotInput(source.record, source.ref, j.anchor(source.record))
		} else {
			file, err = backend.OpenSnapshotOutput(source.record, source.ref, j.anchor(source.record))
		}
		if file != nil {
			opened = append(opened, file)
		}
		if err != nil || file == nil {
			return files, closeFiles, errors.Join(err, errors.New("native snapshot files: source descriptor unavailable"))
		}
		if err := checkNativeSnapshotOutputDescriptor(file, source.record.Identity); err != nil {
			return files, closeFiles, err
		}
		descriptors[i] = file
	}
	frozen, err := backend.FreezeSnapshotDrive(ctx, descriptors[2], directory)
	if frozen != nil {
		opened = append(opened, frozen)
	}
	if err != nil || frozen == nil {
		return files, closeFiles, errors.Join(err, errors.New("native snapshot files: frozen drive unavailable"))
	}
	if err := checkNativeFrozenSnapshotDrive(descriptors[2], frozen); err != nil {
		return files, closeFiles, err
	}
	return nativeSnapshotCaptureFiles{memory: descriptors[0], vmstate: descriptors[1], drive: frozen}, closeFiles, ctx.Err()
}
