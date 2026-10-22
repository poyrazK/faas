package objectnode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"golang.org/x/sys/unix"
)

func openBackupFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrCorrupt
	}
	return f, nil
}

// Copy exactly the declared length with a fixed buffer. A second stat also
// detects a file replacement or ordinary writes while it was being hashed.
func copyBackupFile(ctx context.Context, source, destination string, size int64, buffer []byte) (string, error) {
	return copyBackupFileBudget(ctx, source, destination, size, buffer, nil)
}

func copyBackupFileBudget(ctx context.Context, source, destination string, size int64, buffer []byte, work *replicaWorkBudget) (string, error) {
	f, err := openBackupFile(source)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || before.Size() != size || size < 0 || size > api.MaxObjectUploadBytes {
		return "", ErrCorrupt
	}
	var output *os.File
	h := sha256.New()
	var writer io.Writer = h
	if destination != "" {
		if err = backupSpace(filepath.Dir(destination), size); err != nil {
			return "", err
		}
		output, err = os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		defer func() { _ = output.Close() }()
		writer = io.MultiWriter(output, h)
	}
	var reader io.Reader = f
	if work != nil {
		reader = pacedReplicaReader{ctx: ctx, reader: f, work: work}
	}
	n, err := io.CopyBuffer(writer, io.LimitReader(contextReader{ctx, reader}, size+1), buffer)
	if err != nil {
		return "", err
	}
	after, statErr := f.Stat()
	live, liveErr := os.Lstat(source)
	if n != size || statErr != nil || liveErr != nil || !live.Mode().IsRegular() || scrubFileFingerprint(before) != scrubFileFingerprint(after) || scrubFileFingerprint(after) != scrubFileFingerprint(live) {
		return "", ErrCorrupt
	}
	if output != nil {
		if err = output.Sync(); err != nil {
			return "", err
		}
		if err = output.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), ctx.Err()
}

// The encoded format is canonical JSON. Comparing the decoded value's encoding
// rejects duplicate/unknown/missing fields and ambiguous alternative encodings.
func decodeBackupRecord(body []byte, value any) error {
	if json.Unmarshal(body, value) != nil {
		return ErrCorrupt
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(body, canonical) {
		return ErrCorrupt
	}
	return nil
}

func readBackupInfo(path string) (BackupInfo, error) {
	var info BackupInfo
	f, err := openBackupFile(filepath.Join(path, "backup.json"))
	if err != nil {
		return info, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, api.ObjectNodeBackupRecordMaxBytes+1))
	if err != nil {
		return info, err
	}
	if len(body) > api.ObjectNodeBackupRecordMaxBytes || decodeBackupRecord(body, &info) != nil || info.Version != 1 || info.Engine != "gregale-object-backup" || info.FormatVersion < 3 || info.FormatVersion > 5 || !validReceiptID(info.ID) || info.StartedAt.IsZero() || info.CompletedAt.Before(info.StartedAt) || info.Entries < 1 || info.Entries > api.ObjectNodeBackupMaxEntries || info.Files < 1 || info.Files > info.Entries || info.Bytes < 0 || info.ManifestBytes < 1 || info.ManifestBytes > api.ObjectNodeBackupManifestMaxBytes || !validScrubFindingID(info.ManifestSHA256) {
		return info, ErrCorrupt
	}
	return info, nil
}

func (s *Store) verifyBackupDigest(path string, size int64, digest string) error {
	if strings.HasPrefix(path, "replication/blobs/") {
		if path != "replication/blobs/"+digest+".data" {
			return ErrDigest
		}
		return nil
	}
	if strings.HasSuffix(path, ".data") {
		p := strings.Split(path, "/")
		if len(p) != 5 {
			return ErrCorrupt
		}
		var o Object
		var err error
		if p[0] == "buckets" {
			o, err = readObject(filepath.Join(s.root, filepath.Dir(path)))
			if err == nil {
				err = s.validateBackupObjectReceipt(p[1], o)
			}
		} else {
			n, _ := strconv.ParseInt(p[3], 10, 32) // Validated backup path.
			var part MultipartPart
			part, err = s.readPart(p[1], int32(n))
			o = part.Object
		}
		if err != nil {
			return err
		}
		if p[4] != o.ID+".data" || o.Size != size || o.SHA256 != digest {
			return ErrDigest
		}
	}
	if strings.HasPrefix(path, "scrub/") && strings.HasSuffix(path, ".jsonl") {
		p, err := s.readScrubState()
		if err != nil || p.State != "running" || path != "scrub/"+p.RunID+".jsonl" || size != p.ManifestBytes || digest != p.ManifestSHA256 {
			return ErrCorrupt
		}
	}
	return nil
}

func (s *Store) validateBackupObjectReceipt(bucketHash string, o Object) error {
	id, kind := o.Headers.Get(ReceiptMetadataHeader), ""
	if o.MultipartParts != 0 {
		id, kind = o.Headers.Get(MultipartMetadataHeader), "multipart"
	} else if id == "" {
		return nil
	}
	if !validReceiptID(id) {
		return ErrCorrupt
	}
	r, err := s.readReceipt(id)
	if err != nil || r.Status != ReceiptCommitted || r.Kind != kind || nameHash(r.Bucket) != bucketHash || scrubSeal(r.Object) != scrubSeal(o) {
		return ErrCorrupt
	}
	b, err := s.bucket(r.Bucket)
	if err != nil || b.Generation != r.Generation {
		return ErrCorrupt
	}
	return nil
}

// Read-only validation is used on immutable backup data as well as the source
// after startup recovery. Receipt history need not have a surviving object blob.
func (s *Store) validateBackupCatalog(ctx context.Context) error {
	return s.validateCatalog(ctx, false)
}

func (s *Store) validateCatalog(ctx context.Context, allowFollower bool) error {
	version, err := nodeFormatVersion(s.root)
	if err != nil {
		return err
	}
	if err = s.validateRecoveryCatalog(version); err != nil {
		return err
	}
	if version >= 4 {
		if err = s.validateReplicationCatalog(ctx, allowFollower); err != nil {
			return err
		}
	}
	for _, dir := range []string{"buckets", "receipts", "multipart", "staging", "trash", "scrub", "scrub/findings"} {
		info, err := os.Lstat(filepath.Join(s.root, dir))
		if err != nil || !info.IsDir() {
			return ErrCorrupt
		}
	}
	u, err := s.usage(ctx, false)
	if err != nil {
		return err
	}
	if !u.CatalogHealthy || u.Warnings != 0 {
		return ErrCorrupt
	}
	p, err := s.readScrubState()
	if err != nil {
		return err
	}
	if p.State == "running" {
		info, err := os.Lstat(s.scrubPath(p.RunID + ".jsonl"))
		if err != nil || !info.Mode().IsRegular() || info.Size() != p.ManifestBytes {
			return ErrCorrupt
		}
	}
	if err = visitDirectory(s.scrubPath("findings"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := s.readScrubFinding(strings.TrimSuffix(e.Name(), ".json"))
		return err
	}); err != nil {
		return err
	}
	return visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := s.readReceipt(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return err
		}
		if r.Status == receiptCommitting {
			return errors.New("object backup: unresolved write intent")
		}
		if r.Kind == "multipart" {
			u, err := s.readMultipart(objectmultipart.NativeUploadID(r.ID))
			status := multipartCompleted
			if r.Status == ReceiptRejected {
				status = multipartRejected
			}
			if err != nil || u.Status != status || u.Bucket != r.Bucket || u.Generation != r.Generation || u.Key != r.Object.Key || u.Completion == nil || u.Completion.Hash() != r.ManifestHash {
				return ErrCorrupt
			}
		}
		return nil
	})
}
