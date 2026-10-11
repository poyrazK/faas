//go:build linux

// adr: 794
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Open the retained anonymous inode through its anchor. Neither the jail
// pathname nor a legacy bind map can choose the bytes being published.
func (b linuxNativeImageSources) OpenSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	if err := record.validate(ref.Owner.KernelBootID); err != nil {
		return nil, err
	}
	kind, capture := "mem", strings.TrimSuffix(strings.TrimPrefix(ref.Name, "capture-"), "-mem")
	if strings.HasSuffix(ref.Name, "-vmstate") {
		kind, capture = "vmstate", strings.TrimSuffix(strings.TrimPrefix(ref.Name, "capture-"), "-vmstate")
	}
	name, err := nativeSnapshotOutputName(capture, kind)
	anchor := filepath.Join(b.base, ".native-processes", "image-sources", "points", record.Epoch)
	if err != nil || name != ref.Name || len(record.References) != 1 || record.References[0] != ref || point != anchor ||
		record.Removed || !record.Ready || record.Applied != record.Desired || ref.Removed || !ref.Ready ||
		ref.TargetRemoved || ref.ReadOnly || ref.Link || filepath.Dir(filepath.Dir(filepath.Dir(ref.Root))) != b.base ||
		!nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") {
		return nil, errors.New("native snapshot output: original exclusive capture binding is required")
	}
	if err := b.CheckReference(record, ref); err != nil {
		return nil, err
	}
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return nil, err
	}
	_, metadata, statErr := nativeImageFileMetadata(file)
	if err := errors.Join(statErr, b.CheckAnchor(record, point), checkNativeSnapshotOutputDescriptor(file, record.Identity)); err != nil || metadata != record.Applied {
		return nil, errors.Join(err, file.Close(), errors.New("native snapshot output: original output metadata changed"))
	}
	return file, nil
}
