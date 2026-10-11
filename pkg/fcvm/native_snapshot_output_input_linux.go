//go:build linux

// adr: 568 — native publication never opens a jail output pathname.
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (b linuxNativeImageSources) OpenSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string) (output *os.File, err error) {
	return b.openSnapshotOutput(record, ref, point, false)
}

func (b linuxNativeImageSources) openSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string, handoff bool) (output *os.File, err error) {
	if err := record.validate(ref.Owner.KernelBootID); err != nil {
		return nil, err
	}
	kind, capture := "mem", strings.TrimSuffix(strings.TrimPrefix(ref.Name, "capture-"), "-mem")
	if strings.HasSuffix(ref.Name, "-vmstate") {
		kind, capture = "vmstate", strings.TrimSuffix(strings.TrimPrefix(ref.Name, "capture-"), "-vmstate")
	}
	name, nameErr := nativeSnapshotOutputName(capture, kind)
	anchor := filepath.Join(b.base, ".native-processes", "image-sources", "points", record.Epoch)
	if nameErr != nil || name != ref.Name || len(record.References) != 1 || record.References[0] != ref || point != anchor || record.Removed || !record.Ready || record.Applied != record.Desired || ref.Removed || !ref.Ready || ref.TargetRemoved || ref.ReadOnly || ref.Link || filepath.Dir(filepath.Dir(filepath.Dir(ref.Root))) != b.base || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") {
		return nil, errors.New("native snapshot output input: original exclusive capture binding is required")
	}
	if err := b.CheckReference(record, ref); err != nil {
		return nil, err
	}
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, metadata, err := nativeImageFileMetadata(file)
	if err := errors.Join(err, b.checkAnchor(record, point, handoff)); err != nil || metadata != record.Applied {
		return nil, errors.Join(err, errors.New("native snapshot output input: original output metadata changed"))
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	output = os.NewFile(uintptr(fd), "/proc/self/fd/"+strconv.Itoa(fd))
	return output, nil
}
