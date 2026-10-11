//go:build linux || darwin

// adr: 795 — capture publication consumes the original three-part descriptor set.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// This is the publication half of the native capture adapter. Its caller must
// retain the Manager flight, finish paused snapshot/create and measure the
// pinned machine before entering. It grants no capture completion or serving
// eligibility; the Manager publishes backing and completes the capture journal.
func (v *JailerVMM) publishNativeSnapshotCapture(ctx context.Context, lease Lease, directory string, machine Machine, version string) (info SnapshotInfo, err error) {
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if !ok || v.storage == nil || version == "" || strings.TrimSpace(version) != version || machine.MemSizeMib != lease.MemoryMaxMiB ||
		checkNativeBootDimensions(permit.Physical, machine) != nil {
		return info, ErrSnapshotBackingUnverified
	}
	keys := qualificationSnapshotProof(permit.Incoming, SnapshotInfo{})
	err = v.withNativeSnapshotCaptureFiles(ctx, lease, directory, func(files nativeSnapshotCaptureFiles) error {
		var publishErr error
		info, publishErr = v.publishNativeSnapshotFiles(ctx, files, machine, version, keys.StorageKey, keys.VMStateStorageKey, keys.DriveStorageKey)
		return publishErr
	})
	if err != nil {
		return SnapshotInfo{}, err
	}
	return info, nil
}

// Both callers derive these keys from the original capture permit. The native
// adapter retains its pinned machine session as well as the source locks.
func (v *JailerVMM) publishNativeSnapshotFiles(ctx context.Context, files nativeSnapshotCaptureFiles, machine Machine, version, memoryKey, stateKey, driveKey string) (SnapshotInfo, error) {
	inputs := SnapshotInputs{Version: 1, FCVersion: version, Machine: machine,
		Memory: SnapshotArtifactIdentity{Key: memoryKey}, VMState: SnapshotArtifactIdentity{Key: stateKey}, Drive: SnapshotArtifactIdentity{Key: driveKey}}
	descriptors := []*os.File{files.memory, files.vmstate, files.drive}
	parts := []*SnapshotArtifactIdentity{&inputs.Memory, &inputs.VMState, &inputs.Drive}
	var stamps [3]snapshotInputFileStamp
	for i, file := range descriptors {
		var measureErr error
		*parts[i], stamps[i], measureErr = nativeSnapshotDescriptorIdentity(ctx, file, parts[i].Key)
		if measureErr != nil {
			return SnapshotInfo{}, measureErr
		}
	}
	if err := inputs.Validate(memoryKey); err != nil {
		return SnapshotInfo{}, err
	}
	var stored int64
	for _, part := range parts {
		if stored > math.MaxInt64-part.Bytes {
			return SnapshotInfo{}, ErrSnapshotBackingUnverified
		}
		stored += part.Bytes
	}
	for i, file := range descriptors {
		if err := files.check(ctx); err != nil {
			return SnapshotInfo{}, err
		}
		if err := checkNativeCaptureStamps(descriptors, stamps); err != nil {
			return SnapshotInfo{}, err
		}
		if err := v.putNativeSnapshotDescriptor(ctx, file, *parts[i], stamps[i]); err != nil {
			return SnapshotInfo{}, err
		}
	}
	if err := errors.Join(files.check(ctx), checkNativeCaptureStamps(descriptors, stamps)); err != nil {
		return SnapshotInfo{}, err
	}
	// Logical bytes conservatively account for a backend whose destination
	// allocation cannot be observed through this descriptor capability.
	return SnapshotInfo{Inputs: &inputs, MemBytes: inputs.Memory.Bytes, VMStateBytes: inputs.VMState.Bytes, StoredBytes: stored}, nil
}

func nativeSnapshotDescriptorIdentity(ctx context.Context, file *os.File, key string) (part SnapshotArtifactIdentity, stamp snapshotInputFileStamp, err error) {
	stamp, err = snapshotFileStamp(file)
	if err != nil {
		return part, stamp, err
	}
	h := sha256.New()
	size, err := io.Copy(h, snapshotContextReader{ctx: ctx, Reader: io.NewSectionReader(file, 0, stamp.Size)})
	after, statErr := snapshotFileStamp(file)
	if err = errors.Join(err, statErr, ctx.Err()); err != nil {
		return part, stamp, err
	}
	if after != stamp || size != stamp.Size {
		return part, stamp, ErrSnapshotBackingChanged
	}
	return SnapshotArtifactIdentity{Key: key, Bytes: size, SHA256: hex.EncodeToString(h.Sum(nil))}, stamp, nil
}

func checkNativeCaptureStamps(files []*os.File, stamps [3]snapshotInputFileStamp) error {
	for i, file := range files {
		stamp, err := snapshotFileStamp(file)
		if err != nil || stamp != stamps[i] {
			return errors.Join(err, ErrSnapshotBackingChanged)
		}
	}
	return nil
}

func (v *JailerVMM) putNativeSnapshotDescriptor(ctx context.Context, file *os.File, part SnapshotArtifactIdentity, stamp snapshotInputFileStamp) error {
	h := sha256.New()
	// Publish through the measured descriptor. A backend cannot reopen a jail
	// path or bypass the digest by treating the reader as a named file.
	r := &nativeSnapshotCountingReader{Reader: io.TeeReader(snapshotContextReader{ctx: ctx, Reader: io.NewSectionReader(file, 0, part.Bytes)}, h)}
	if err := v.storage.Put(ctx, part.Key, r); err != nil {
		return fmt.Errorf("native snapshot: publish %s: %w", part.Key, err)
	}
	after, err := snapshotFileStamp(file)
	if err != nil || after != stamp || r.bytes != part.Bytes || hex.EncodeToString(h.Sum(nil)) != part.SHA256 {
		return errors.Join(err, ErrSnapshotBackingChanged)
	}
	return ctx.Err()
}

type nativeSnapshotCountingReader struct {
	io.Reader
	bytes int64
}

func (r *nativeSnapshotCountingReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.bytes += int64(n)
	return n, err
}
