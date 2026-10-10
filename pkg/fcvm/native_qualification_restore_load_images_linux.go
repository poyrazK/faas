//go:build linux

package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (b linuxNativeImageSources) OpenRestoreImage(source nativeImageSourceRecord, ref nativeImageReference, point string, captured *nativeSnapshotBackingImage) (*os.File, error) {
	if err := source.validate(ref.Owner.KernelBootID); err != nil {
		return nil, err
	}
	if len(source.References) != 1 || source.References[0] != ref || source.Removed || !source.Ready || source.Applied != source.Desired ||
		ref.Removed || ref.TargetRemoved || !ref.Ready || ref.Link || point != filepath.Join(b.base, ".native-processes", "image-sources", "points", source.Epoch) {
		return nil, errors.New("native restore load: original exclusive target descriptor is required")
	}
	claim, err := b.diskStagingClaim(source, point)
	if err != nil || claim == nil || claim.Version != 3 {
		return nil, errors.Join(err, errors.New("native restore load: original anonymous disk claim is required"))
	}
	if captured != nil {
		if claim.Backing == nil || *claim.Backing != *captured || ref.Name != captured.Name || !ref.ReadOnly {
			return nil, errors.New("native restore load: captured backing witness changed")
		}
	} else if claim.Backing != nil || !nativeRestoreImageName(ref.Name) || ref.ReadOnly != (ref.Name != layerImageName) {
		return nil, errors.New("native restore load: original memory/state/private-drive profile is required")
	}
	if err := errors.Join(b.CheckAnchor(source, point), b.CheckReference(source, ref)); err != nil {
		return nil, err
	}
	file, err := openNativeImageAnchor(source, point)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	_, metadata, metadataErr := nativeImageFileMetadata(file)
	if err := errors.Join(unix.Fstat(int(file.Fd()), &stat), metadataErr); err != nil || stat.Nlink != 0 || metadata != source.Applied {
		return nil, errors.Join(err, file.Close(), errors.New("native restore load: target descriptor lost anonymous identity or permissions"))
	}
	return file, nil
}

func verifyNativeRestoreLoadDescriptor(ctx context.Context, file *os.File, source nativeImageSourceRecord, expected nativeSnapshotBackingImage) error {
	identity, _, err := nativeImageFileMetadata(file)
	before, statErr := file.Stat()
	if err != nil || statErr != nil || identity != source.Identity || identity != expected.Identity || !before.Mode().IsRegular() || before.Size() != expected.LogicalBytes {
		return errors.Join(err, statErr, errors.New("native restore load: descriptor differs from original image witness"))
	}
	if err := verifyNativeRestoreDigest(ctx, file, expected.LogicalBytes, expected.SHA256); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return errors.Join(err, errors.New("native restore load: input changed during verification"))
	}
	return ctx.Err()
}
