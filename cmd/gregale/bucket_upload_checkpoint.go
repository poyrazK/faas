package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const bucketUploadCheckpointVersion = 1

// This contains no signed URLs, native IDs or credentials. A saved ETag is
// trusted only when both the source hashes and the provider's part agree.
type bucketUploadCheckpoint struct {
	Version     int                          `json:"version"`
	APIBase     string                       `json:"api_base"`
	App         string                       `json:"app"`
	Bucket      string                       `json:"bucket"`
	UploadID    string                       `json:"upload_id"`
	Key         string                       `json:"key"`
	Size        int64                        `json:"size_bytes"`
	ContentType string                       `json:"content_type"`
	SHA256      string                       `json:"sha256"`
	PartSize    int64                        `json:"part_size_bytes"`
	Phase       string                       `json:"phase"`
	Parts       []bucketUploadCheckpointPart `json:"parts"`
}

type bucketUploadCheckpointPart struct {
	SHA256    string `json:"sha256"`
	Attempted bool   `json:"attempted,omitempty"`
	ETag      string `json:"etag,omitempty"`
}

func validBucketUploadID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func bucketUploadCheckpointPath(base, id string) (string, error) {
	if !validBucketUploadID(id) || base == "" {
		return "", errors.New("invalid upload checkpoint identity")
	}
	if _, err := validateConfigAPIBase(base); err != nil {
		return "", errors.New("upload checkpoint API endpoint cannot contain credentials, query parameters or fragments")
	}
	dir, err := uploadStateDir()
	if err != nil {
		return "", err
	}
	// Keep object records outside the deployment upload cache's cleanup scope.
	key := sha256.Sum256([]byte(base + "\x00" + id))
	return filepath.Join(filepath.Dir(dir), "object-uploads", hex.EncodeToString(key[:])+".json"), nil
}

func lockBucketUploadCheckpoint(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create object upload checkpoint directory: %w", err)
	}
	// Do not unlink lock files: another process could still hold the old inode.
	if info, err := os.Lstat(path + ".lock"); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("invalid object upload checkpoint lock")
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close()
		return nil, errors.New("object upload checkpoint is locked by another Gregale process")
	}
	return lock, nil
}

func loadBucketUploadCheckpoint(path string) (bucketUploadCheckpoint, error) {
	var cp bucketUploadCheckpoint
	f, err := openCustomerFile(path)
	if err != nil {
		return cp, fmt.Errorf("open object upload checkpoint (use the original CLI state directory): %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() > api.MaxObjectMultipartCheckpointBytes || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return cp, errors.New("object upload checkpoint must be private and bounded")
	}
	decoder := json.NewDecoder(io.LimitReader(f, api.MaxObjectMultipartCheckpointBytes+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cp); err != nil {
		return cp, fmt.Errorf("decode object upload checkpoint: %w", err)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return cp, errors.New("object upload checkpoint contains trailing data")
	}
	if cp.Version != bucketUploadCheckpointVersion || !validBucketUploadID(cp.UploadID) || cp.Size <= 0 || cp.Size > api.MaxObjectUploadBytes || cp.PartSize <= 0 || cp.PartSize > api.MaxObjectSinglePutBytes || len(cp.Parts) < 1 || len(cp.Parts) > api.MaxMultipartParts || int64(len(cp.Parts)) != (cp.Size-1)/cp.PartSize+1 || !validBucketFileHash(cp.SHA256) {
		return cp, errors.New("invalid object upload checkpoint geometry or fingerprint")
	}
	if cp.Phase != "uploading" && cp.Phase != "completing" && cp.Phase != "completed" {
		return cp, errors.New("invalid object upload checkpoint phase")
	}
	for _, part := range cp.Parts {
		if !validBucketFileHash(part.SHA256) || part.ETag != "" && (!part.Attempted || !validBucketPartETag(part.ETag)) || cp.Phase != "uploading" && part.ETag == "" {
			return cp, errors.New("invalid object upload checkpoint part")
		}
	}
	return cp, nil
}

func saveBucketUploadCheckpoint(path string, cp bucketUploadCheckpoint) error {
	data, err := json.Marshal(cp)
	if err != nil || len(data) > api.MaxObjectMultipartCheckpointBytes {
		return errors.New("invalid object upload checkpoint size")
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("invalid object upload checkpoint destination")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".object-upload-*.tmp")
	if err != nil {
		return fmt.Errorf("create object upload checkpoint: %w", err)
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(append(data, '\n')); err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil && runtime.GOOS != "windows" {
		// Persist the rename before authorizing the next provider operation.
		var dir *os.File
		dir, err = os.OpenFile(filepath.Dir(path), os.O_RDONLY, 0)
		if err == nil {
			err = dir.Sync()
			_ = dir.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("persist object upload checkpoint: %w", err)
	}
	return nil
}

func validBucketFileHash(s string) bool {
	digest, err := hex.DecodeString(s)
	return err == nil && len(digest) == sha256.Size && s == strings.ToLower(s)
}

type bucketContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r bucketContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func bucketFileFingerprint(ctx context.Context, file *os.File, size, partSize int64) (string, []bucketUploadCheckpointPart, error) {
	whole := sha256.New()
	var parts []bucketUploadCheckpointPart
	if partSize <= 0 {
		partSize = max(size, 1)
	}
	for offset := int64(0); offset < size; offset += partSize {
		part := sha256.New()
		length := min(partSize, size-offset)
		n, err := io.Copy(io.MultiWriter(whole, part), bucketContextReader{ctx, io.NewSectionReader(file, offset, length)})
		if err != nil || n != length {
			return "", nil, errors.New("upload source changed or fingerprinting was interrupted")
		}
		parts = append(parts, bucketUploadCheckpointPart{SHA256: hex.EncodeToString(part.Sum(nil))})
	}
	info, err := file.Stat()
	if err != nil || info.Size() != size {
		return "", nil, errors.New("upload source size changed")
	}
	return hex.EncodeToString(whole.Sum(nil)), parts, nil
}

func discardBucketStagedPart(checkpointPath string) error {
	path := checkpointPath + ".part"
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid staged object upload part")
	}
	if err = os.Remove(path); err != nil {
		return fmt.Errorf("remove interrupted object upload part: %w", err)
	}
	return nil
}

func stageBucketUploadPart(ctx context.Context, file *os.File, offset, size int64, hash, checkpointPath string) (*os.File, error) {
	// The session lock covers this private staging path. Exclusive creation
	// refuses links; a process killed mid-transfer leaves only this one file,
	// which the next explicit resume removes after validating its checkpoint.
	tmp, err := os.OpenFile(checkpointPath+".part", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("stage object upload part: %w", err)
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, digest), bucketContextReader{ctx, io.NewSectionReader(file, offset, size)})
	if err == nil && n == size && hex.EncodeToString(digest.Sum(nil)) == hash {
		_, err = tmp.Seek(0, io.SeekStart)
		if err == nil {
			return tmp, nil
		}
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return nil, errors.New("upload source changed or part staging was interrupted")
}
