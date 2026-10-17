package objectnode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

type MultipartPart struct {
	Number int32  `json:"number"`
	Object Object `json:"object"`
	Seal   string `json:"seal"`
}

func sealPart(p MultipartPart) MultipartPart {
	p.Seal = ""
	body, _ := json.Marshal(p)
	sum := sha256.Sum256(body)
	p.Seal = hex.EncodeToString(sum[:])
	return p
}

func (s *Store) partPath(id string, number int32) string {
	return filepath.Join(s.multipartPath(id), "parts", strconv.FormatInt(int64(number), 10))
}

func (s *Store) readPart(id string, number int32) (MultipartPart, error) {
	var part MultipartPart
	if number < 1 || number > api.MaxMultipartParts {
		return part, ErrInvalidPart
	}
	err := readJSON(filepath.Join(s.partPath(id, number), "current.json"), &part)
	if errors.Is(err, os.ErrNotExist) {
		return part, ErrInvalidPart
	}
	o := part.Object
	sha, decodeErr := hex.DecodeString(o.SHA256)
	if err != nil || part.Seal != sealPart(part).Seal || part.Number != number || o.Version != 1 || !validReceiptID(o.ID) || !validKey(o.Key) || o.Size < 0 || o.Size > api.MaxObjectSinglePutBytes || o.Modified.IsZero() || len(o.ETag) != 34 || decodeErr != nil || len(sha) != 32 || o.MultipartParts != 0 || o.ChecksumAlgorithm != "" && !objectchecksum.ValidPart(o.ChecksumAlgorithm, o.Checksum) {
		return part, ErrCorrupt
	}
	return part, nil
}

func (s *Store) multipartParts(id string) ([]MultipartPart, error) {
	parts := make([]MultipartPart, 0)
	err := visitDirectory(filepath.Join(s.multipartPath(id), "parts"), func(e os.DirEntry) error {
		number, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || strconv.FormatInt(number, 10) != e.Name() || number < 1 || number > api.MaxMultipartParts {
			return ErrCorrupt
		}
		part, err := s.readPart(id, int32(number))
		if errors.Is(err, ErrInvalidPart) {
			return nil
		}
		if err != nil {
			return err
		}
		parts = append(parts, part)
		return nil
	})
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return parts, err
}

func (s *Store) UploadPart(ctx context.Context, bucket, key, id string, number int32, body io.Reader, size int64, options PutOptions) (MultipartPart, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return MultipartPart{}, ErrClosed
	}
	if number < 1 || number > api.MaxMultipartParts || size < 0 || size > api.MaxObjectSinglePutBytes || options.Receipt != "" || len(options.Headers) != 0 || options.IfMatch != "" || options.IfNoneMatch != "" {
		return MultipartPart{}, ErrInvalid
	}
	s.mu.RLock()
	u, err := s.upload(bucket, key, id)
	s.mu.RUnlock()
	if err != nil {
		return MultipartPart{}, err
	}
	if u.Status != multipartActive || !time.Now().Before(u.ExpiresAt) {
		return MultipartPart{}, ErrNoUpload
	}
	if !u.Checksum.Empty() && (options.ChecksumAlgorithm != u.Checksum.Algorithm || options.Checksum == "") {
		return MultipartPart{}, ErrInvalid
	}
	admitted, err := s.beginAdmission("part", size, "")
	if err != nil {
		return MultipartPart{}, err
	}
	defer s.releaseAdmission(admitted)
	stageID := uuid.NewString()
	stage := filepath.Join(s.root, "staging", stageID)
	defer func() { _ = os.Remove(stage) }()
	o, err := stageObject(ctx, stage, key, stageID, body, size, options)
	if err != nil {
		return MultipartPart{}, err
	}
	o.Modified = time.Now().UTC()
	part := sealPart(MultipartPart{Number: number, Object: o})
	prepared, err := s.prepareReplay(ctx, stage, o)
	if err != nil {
		return MultipartPart{}, err
	}
	defer prepared.close()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.bindPreparedReplay(prepared)()
	if err = s.finishPending(false); err != nil {
		return MultipartPart{}, err
	}
	current, err := s.upload(bucket, key, id)
	if err != nil {
		return MultipartPart{}, err
	}
	if current.Status != multipartActive || !time.Now().Before(current.ExpiresAt) || current.Generation != u.Generation {
		return MultipartPart{}, ErrNoUpload
	}
	parts, err := s.multipartParts(id)
	if err != nil {
		return MultipartPart{}, err
	}
	total := size
	for _, existing := range parts {
		if existing.Number != number {
			if existing.Object.Size > current.MaxBytes-total {
				return MultipartPart{}, ErrCapacity
			}
			total += existing.Object.Size
		}
	}
	if total > current.MaxBytes {
		return MultipartPart{}, ErrCapacity
	}
	if err = ctx.Err(); err != nil {
		return MultipartPart{}, err
	}
	// An initiation whose final fsync failed can be discovered by its stable ID.
	// Make that session record durable before acknowledging any of its parts.
	if err = syncDirectory(s.multipartPath(id)); err != nil {
		return MultipartPart{}, err
	}
	dir := s.partPath(id, number)
	if err = os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return MultipartPart{}, err
	}
	if err = syncDirectory(filepath.Dir(dir)); err != nil {
		return MultipartPart{}, err
	}
	if err = s.moveStagedBlob(stage, filepath.Join(dir, o.ID+".data")); err != nil {
		return MultipartPart{}, err
	}
	if err = syncDirectory(dir); err != nil {
		return MultipartPart{}, err
	}
	if _, err = s.writeRecord(filepath.Join(dir, "current.json"), part); err != nil {
		return MultipartPart{}, err
	}
	for _, previous := range parts {
		if previous.Number == number {
			_ = os.Remove(filepath.Join(dir, previous.Object.ID+".data"))
		}
	}
	return part, nil
}

func (s *Store) ListParts(bucket, key, id string, marker int32, limit int) ([]MultipartPart, bool, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, false, ErrClosed
	}
	if marker < 0 || marker > api.MaxMultipartParts || limit < 0 || limit > api.MaxObjectS3ListItems {
		return nil, false, ErrInvalid
	}
	u, err := s.upload(bucket, key, id)
	if err != nil {
		return nil, false, err
	}
	if u.Status != multipartActive && u.Status != multipartCompleting || !time.Now().Before(u.ExpiresAt) {
		return nil, false, ErrNoUpload
	}
	if limit == 0 {
		return []MultipartPart{}, false, nil
	}
	parts, err := s.multipartParts(id)
	if err != nil {
		return nil, false, err
	}
	first := sort.Search(len(parts), func(i int) bool { return parts[i].Number > marker })
	parts = parts[first:]
	truncated := len(parts) > limit
	if truncated {
		parts = parts[:limit]
	}
	return parts, truncated, nil
}

type MultipartListRequest struct {
	Prefix, KeyMarker, UploadMarker string
	Limit                           int
}

func (s *Store) ListMultipart(ctx context.Context, bucket string, r MultipartListRequest) ([]MultipartUpload, bool, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, false, ErrClosed
	}
	if r.Limit < 0 || r.Limit > api.MaxObjectS3ListItems || len(r.Prefix) > api.MaxObjectS3ListTextBytes || len(r.KeyMarker) > api.MaxObjectS3ListTextBytes || r.UploadMarker != "" && (!validReceiptID(r.UploadMarker) || r.KeyMarker == "") {
		return nil, false, ErrInvalid
	}
	b, err := s.bucket(bucket)
	if err != nil {
		return nil, false, err
	}
	if r.Limit == 0 {
		return []MultipartUpload{}, false, nil
	}
	items := make([]MultipartUpload, 0)
	err = visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		u, err := s.readMultipart(e.Name())
		if errors.Is(err, ErrNoUpload) {
			return nil
		}
		if err != nil {
			return err
		}
		if u.Bucket != bucket || u.Generation != b.Generation || u.Status != multipartActive && u.Status != multipartCompleting || !time.Now().Before(u.ExpiresAt) || !strings.HasPrefix(u.Key, r.Prefix) || u.Key < r.KeyMarker || u.Key == r.KeyMarker && (r.UploadMarker == "" || u.ID <= r.UploadMarker) {
			return nil
		}
		// Listings retain only the bounded summaries, never completion manifests.
		u.Headers, u.Completion = nil, nil
		items = append(items, u)
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		return items[i].Key < items[j].Key || items[i].Key == items[j].Key && items[i].ID < items[j].ID
	})
	truncated := len(items) > r.Limit
	if truncated {
		items = items[:r.Limit]
	}
	return items, truncated, err
}
