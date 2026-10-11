// adr: 568 — only the target's own exclusive image epochs can supply a load.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

type nativeQualificationRestoreImageBackend interface {
	OpenRestoreImage(nativeImageSourceRecord, nativeImageReference, string, *nativeSnapshotBackingImage) (*os.File, error)
}

func (j *nativeImageSourceJournal) restoreLoadReferences(owner nativeLaunchRecord, root string, retained bool) (map[string]nativeImageSourceRecord, map[string]nativeImageReference, error) {
	records, err := j.records()
	if err != nil {
		return nil, nil, err
	}
	sources, refs := make(map[string]nativeImageSourceRecord), make(map[string]nativeImageReference)
	for _, source := range records {
		for _, ref := range source.References {
			if !sameNativeImageOwner(ref, owner) {
				continue
			}
			if refs[ref.Name].ID != "" || len(source.References) != 1 || !source.Ready || !ref.Ready || ref.Link ||
				!nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") || root != "" && ref.Root != root ||
				!retained && (source.Removed || ref.Removed || ref.TargetRemoved || source.Applied != source.Desired) {
				return nil, nil, errors.New("native restore load: original exclusive target image binding changed")
			}
			if root == "" {
				root = ref.Root
			}
			sources[ref.Name], refs[ref.Name] = source, ref
		}
	}
	if len(refs) != 5 {
		return nil, nil, errors.New("native restore load: exactly five target image bindings are required")
	}
	return sources, refs, nil
}

func (j *nativeImageSourceJournal) requireRestoreLoadWitnesses(r nativeQualificationRestoreLoadRecord, owner nativeLaunchRecord, root string, retained bool) error {
	sources, refs, err := j.restoreLoadReferences(owner, root, retained)
	if err != nil {
		return err
	}
	for i, image := range r.Images {
		source, ref := sources[image.Name], refs[image.Name]
		if source.Epoch != image.Epoch || source.Identity != image.Identity || ref.ID != image.ReferenceID || ref.ReadOnly != (i != 4) {
			return errors.New("native restore load: effect image witness lost its original target epoch")
		}
		if !retained {
			if j.backend == nil {
				return errors.New("native restore load: original image adapter is unavailable")
			}
			if err := errors.Join(j.backend.CheckAnchor(source, j.anchor(source)), j.backend.CheckReference(source, ref)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (j *nativeImageSourceJournal) verifyRestoreLoadImage(ctx context.Context, source nativeImageSourceRecord, ref nativeImageReference, expected nativeSnapshotBackingImage, captured *nativeSnapshotBackingImage) (result error) {
	backend, ok := j.backend.(nativeQualificationRestoreImageBackend)
	if !ok {
		return errors.New("native restore load: original descriptor adapter is unavailable")
	}
	lock, err := j.lock(ctx, source.Identity)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	current, err := j.records()
	if err != nil {
		return err
	}
	found := false
	for _, record := range current {
		if record.Epoch == source.Epoch && record.Identity == source.Identity && record.Ready && !record.Removed && record.Applied == source.Applied {
			for _, original := range record.References {
				found = found || original == ref
			}
		}
	}
	if !found {
		return errors.New("native restore load: original source changed before descriptor read")
	}
	file, err := backend.OpenRestoreImage(source, ref, j.anchor(source), captured)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	return verifyNativeRestoreLoadDescriptor(ctx, file, source, expected)
}
