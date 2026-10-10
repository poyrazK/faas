// adr: 568 — kernel/base evidence is read through original source anchors.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

type nativeSnapshotBackingInputBackend interface {
	OpenSnapshotBacking(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
}

type nativeSnapshotBackingBinding struct {
	source nativeImageSourceRecord
	ref    nativeImageReference
}

// Capture currently accepts the two-drive, single-workload profile. Extra
// read-only drives need their own captured path/content contract before restore.
func (j *nativeImageSourceJournal) snapshotBackings(owner nativeLaunchRecord, root string) ([]nativeSnapshotBackingBinding, error) {
	records, err := j.records()
	if err != nil {
		return nil, err
	}
	var bindings []nativeSnapshotBackingBinding
	for _, source := range records {
		for _, ref := range source.References {
			if !sameNativeImageOwner(ref, owner) || ref.Root != root || ref.Removed || !ref.ReadOnly {
				continue
			}
			if source.Removed || !source.Ready || source.Applied != source.Desired || !ref.Ready || ref.TargetRemoved || !nativeSnapshotBackingName(ref.Name) {
				return nil, errors.New("native snapshot backing: original read-only binding is incomplete")
			}
			bindings = append(bindings, nativeSnapshotBackingBinding{source: source, ref: ref})
		}
	}
	if len(bindings) != 2 {
		return nil, errors.New("native snapshot backing: exactly the original kernel and base bindings are required")
	}
	return bindings, nil
}

// Caller retains original incoming and physical locks. Each source descriptor
// closes before its epoch lock; no jail pathname or current artifact resolver
// can substitute bytes or names for the capture's original references.
func (j *nativeImageSourceJournal) captureBackingLocked(ctx context.Context, owner nativeLaunchRecord, root string, original nativeSnapshotBackingBinding) (image nativeSnapshotBackingImage, result error) {
	backend, ok := j.backend.(nativeSnapshotBackingInputBackend)
	if !ok {
		return image, errors.New("native snapshot backing: pinned read-only input backend is unavailable")
	}
	lock, err := j.lock(ctx, original.source.Identity)
	if err != nil {
		return image, err
	}
	defer func() { result = errors.Join(result, lock.Close(), ctx.Err()) }()
	bindings, err := j.snapshotBackings(owner, root)
	if err != nil {
		return image, err
	}
	found := false
	for _, binding := range bindings {
		if binding.ref != original.ref {
			continue
		}
		source, prior := binding.source, original.source
		found = source.Epoch == prior.Epoch && source.Identity == prior.Identity && source.Namespace == prior.Namespace && source.Applied == prior.Applied && source.Desired == prior.Desired
	}
	if !found {
		return image, errors.New("native snapshot backing: original image epoch changed")
	}
	file, err := backend.OpenSnapshotBacking(original.source, original.ref, j.anchor(original.source))
	if file != nil {
		defer func() { result = errors.Join(result, file.Close()) }()
	}
	if err != nil || file == nil {
		return image, errors.Join(err, errors.New("native snapshot backing: original descriptor is unavailable"))
	}
	info, err := file.Stat()
	if err != nil {
		return image, err
	}
	identity, err := resourceFileID(info)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || nativeLoopIdentity(identity) != original.source.Identity {
		return image, errors.Join(err, errors.New("native snapshot backing: descriptor differs from original image"))
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, nativeSnapshotBackingReader{ctx: ctx, reader: io.NewSectionReader(file, 0, info.Size())}, make([]byte, 128*1024))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || n != info.Size() || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
		return image, errors.Join(err, statErr, errors.New("native snapshot backing: original image changed during hash"))
	}
	image = nativeSnapshotBackingImage{Epoch: original.source.Epoch, ReferenceID: original.ref.ID, Identity: original.source.Identity,
		Name: original.ref.Name, LogicalBytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	return image, errors.Join(j.backend.CheckAnchor(original.source, j.anchor(original.source)), j.backend.CheckReference(original.source, original.ref), ctx.Err())
}

type nativeSnapshotBackingReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r nativeSnapshotBackingReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}
