package fcvm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type capturedSnapshotFile struct {
	key, path string
	file      *os.File
	info      os.FileInfo
	identity  rootfs.ArtifactIdentity
}

func removeNativeSnapshotOutputs(root string) error {
	var result error
	for _, name := range []string{"mem", "vmstate"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func pinCapturedSnapshotFiles(ctx context.Context, spec SnapshotSpec, root, frozen string) ([]capturedSnapshotFile, error) {
	paths := snapshotFilePaths(root, frozen)
	keys := []string{spec.StorageKey, spec.VMStateStorageKey, state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey})}
	files := make([]capturedSnapshotFile, 0, len(paths))
	for i, path := range paths {
		file, err := pinCapturedSnapshotFile(ctx, keys[i], path)
		if err != nil {
			closeCapturedSnapshotFiles(files)
			return nil, err
		}
		for _, previous := range files {
			if os.SameFile(previous.info, file.info) {
				_ = file.file.Close()
				closeCapturedSnapshotFiles(files)
				return nil, runtimeadmission.ErrInvalid
			}
		}
		files = append(files, file)
	}
	return files, nil
}

func pinCapturedSnapshotFile(ctx context.Context, key, path string) (capturedSnapshotFile, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > api.ApplicationStandardSnapshotMaxArtifactBytes {
		return capturedSnapshotFile{}, errors.Join(runtimeadmission.ErrInvalid, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return capturedSnapshotFile{}, err
	}
	identity, info, err := measurePinnedRuntimeDrive(ctx, file, before.Size())
	if err == nil && (!os.SameFile(before, info) || !before.ModTime().Equal(info.ModTime())) {
		err = runtimeadmission.ErrStale
	}
	if err != nil {
		return capturedSnapshotFile{}, errors.Join(err, file.Close())
	}
	return capturedSnapshotFile{key: key, path: path, file: file, info: info, identity: identity}, nil
}

func closeCapturedSnapshotFiles(files []capturedSnapshotFile) {
	for _, file := range files {
		_ = file.file.Close()
	}
}

type measuredSnapshotReader struct {
	ctx   context.Context
	input io.Reader
	hash  hash.Hash
	bytes int64
}

func (r *measuredSnapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
		r.bytes += int64(n)
	}
	return n, err
}

// Hash the bytes consumed by Put, including sparse zero-filled regions. A
// backend returning success after a prefix cannot produce capture evidence.
func publishMeasuredSnapshotFile(ctx context.Context, backend storage.StorageBackend, file capturedSnapshotFile) error {
	if err := checkCapturedSnapshotFile(file); err != nil {
		return err
	}
	reader := &measuredSnapshotReader{ctx: ctx, input: io.NewSectionReader(file.file, 0, file.identity.Bytes+1), hash: sha256.New()}
	if err := backend.Put(ctx, file.key, reader); err != nil {
		return fmt.Errorf("vmm: publish measured snapshot artifact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reader.bytes != file.identity.Bytes || fmt.Sprintf("sha256:%x", reader.hash.Sum(nil)) != file.identity.Digest {
		return errors.Join(runtimeadmission.ErrInvalid, io.ErrUnexpectedEOF)
	}
	var probe [1]byte
	if n, err := reader.Read(probe[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.Join(runtimeadmission.ErrInvalid, err)
	}
	return checkCapturedSnapshotFile(file)
}

func checkCapturedSnapshotFile(file capturedSnapshotFile) error {
	pathInfo, err := os.Lstat(file.path)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, file.info) {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	info, err := file.file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, file.info) || info.Size() != file.identity.Bytes || !info.ModTime().Equal(file.info.ModTime()) {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	return nil
}
