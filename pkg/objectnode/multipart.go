package objectnode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

const MultipartMetadataHeader = "X-Amz-Meta-Gregale-Upload-Id"

const (
	multipartActive     = "active"
	multipartCompleting = "completing"
	multipartCompleted  = "completed"
	multipartRejected   = "rejected"
	multipartAborted    = "aborted"
	multipartExpired    = "expired"
)

type MultipartOptions struct {
	Headers  http.Header
	Checksum objectchecksum.Policy
	MaxBytes int64
}

type MultipartUpload struct {
	Version    int                       `json:"version"`
	ID         string                    `json:"id"`
	ReceiptID  string                    `json:"receipt_id"`
	Bucket     string                    `json:"bucket"`
	Generation string                    `json:"generation"`
	Key        string                    `json:"key"`
	Status     string                    `json:"status"`
	CreatedAt  time.Time                 `json:"created_at"`
	ExpiresAt  time.Time                 `json:"expires_at"`
	Headers    http.Header               `json:"headers"`
	Checksum   objectchecksum.Policy     `json:"checksum"`
	MaxBytes   int64                     `json:"max_bytes"`
	Completion *objectmultipart.Manifest `json:"completion,omitempty"`
	Seal       string                    `json:"seal"`
	SettledAt  *time.Time                `json:"settled_at,omitempty"`
}

func (s *Store) multipartPath(id string) string { return filepath.Join(s.root, "multipart", id) }

func sealMultipart(u MultipartUpload) MultipartUpload {
	u.Seal = ""
	body, _ := json.Marshal(u)
	sum := sha256.Sum256(body)
	u.Seal = hex.EncodeToString(sum[:])
	return u
}

func (s *Store) readMultipart(id string) (MultipartUpload, error) {
	var u MultipartUpload
	if !validReceiptID(id) {
		return u, ErrInvalid
	}
	err := readJSON(filepath.Join(s.multipartPath(id), "session.json"), &u)
	if errors.Is(err, os.ErrNotExist) {
		return u, ErrNoUpload
	}
	if err != nil || u.Version != 1 || u.ID != id || !validReceiptID(u.ReceiptID) || objectmultipart.NativeUploadID(u.ReceiptID) != id || !ValidBucket(u.Bucket) || !validReceiptID(u.Generation) || !validKey(u.Key) || u.CreatedAt.IsZero() || !u.ExpiresAt.After(u.CreatedAt) || !u.Checksum.Valid() || u.MaxBytes < 1 || u.MaxBytes > api.MaxObjectUploadBytes || u.Headers.Get(MultipartMetadataHeader) != u.ReceiptID || u.Seal != sealMultipart(u).Seal || !validMultipartSettlement(u) {
		return u, ErrCorrupt
	}
	switch u.Status {
	case multipartActive, multipartAborted, multipartExpired:
		if u.Status == multipartActive && u.Completion != nil {
			return u, ErrCorrupt
		}
	case multipartCompleting, multipartCompleted, multipartRejected:
		if u.Completion == nil || u.Completion.UploadID != id || !validManifest(*u.Completion, u.Checksum, u.MaxBytes) {
			return u, ErrCorrupt
		}
	default:
		return u, ErrCorrupt
	}
	return u, nil
}

func (s *Store) writeMultipart(u MultipartUpload) error {
	_, err := s.writeRecord(filepath.Join(s.multipartPath(u.ID), "session.json"), sealMultipart(u))
	return err
}

func (s *Store) upload(bucket, key, id string) (MultipartUpload, error) {
	u, err := s.readMultipart(id)
	if err != nil {
		return u, err
	}
	if u.Bucket != bucket || u.Key != key {
		return u, ErrNoUpload
	}
	b, err := s.bucket(bucket)
	if err != nil {
		return u, err
	}
	if u.Generation != b.Generation {
		return u, ErrNoUpload
	}
	return u, nil
}

func (s *Store) MultipartInfo(bucket, key, id string) (MultipartUpload, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return MultipartUpload{}, ErrClosed
	}
	return s.upload(bucket, key, id)
}

func (s *Store) CreateMultipart(bucket, key string, options MultipartOptions) (MultipartUpload, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MultipartUpload{}, ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return MultipartUpload{}, err
	}
	headers := make(http.Header, len(options.Headers)+1)
	for name, values := range options.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := headers[canonical]; duplicate {
			return MultipartUpload{}, ErrInvalid
		}
		headers[canonical] = append([]string(nil), values...)
	}
	if !validKey(key) || !options.Checksum.Valid() || headers.Get(ReceiptMetadataHeader) != "" {
		return MultipartUpload{}, ErrInvalid
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = api.MaxObjectUploadBytes
	}
	if options.MaxBytes < 1 || options.MaxBytes > api.MaxObjectUploadBytes {
		return MultipartUpload{}, ErrInvalid
	}
	b, err := s.bucket(bucket)
	if err != nil {
		return MultipartUpload{}, err
	}
	receiptID := headers.Get(MultipartMetadataHeader)
	if receiptID == "" {
		receiptID = uuid.NewString()
	}
	if !validReceiptID(receiptID) {
		return MultipartUpload{}, ErrInvalid
	}
	id := objectmultipart.NativeUploadID(receiptID)
	headers.Set(MultipartMetadataHeader, receiptID)
	if existing, e := s.readMultipart(id); !errors.Is(e, ErrNoUpload) {
		if e != nil {
			return MultipartUpload{}, e
		}
		if existing.Bucket != bucket || existing.Generation != b.Generation || existing.Key != key || existing.Checksum != options.Checksum || existing.MaxBytes != options.MaxBytes || !reflect.DeepEqual(existing.Headers, headers) {
			return MultipartUpload{}, ErrReceiptReuse
		}
		if existing.Status != multipartActive || !time.Now().Before(existing.ExpiresAt) {
			return MultipartUpload{}, ErrNoUpload
		}
		if err = syncDirectory(s.multipartPath(id)); err != nil {
			return MultipartUpload{}, err
		}
		return existing, nil
	}
	if _, e := s.readReceipt(receiptID); !errors.Is(e, ErrNoReceipt) {
		if e != nil {
			return MultipartUpload{}, e
		}
		return MultipartUpload{}, ErrReceiptReuse
	}
	if err = s.checkNewReceiptID(receiptID); err != nil {
		return MultipartUpload{}, err
	}
	active, err := s.activeMultipartCount(bucket, b.Generation)
	if err != nil {
		return MultipartUpload{}, err
	}
	if active >= api.MaxActiveMultipartUploadsPerBucket {
		return MultipartUpload{}, ErrCapacity
	}
	admitted, err := s.admitWriteLocked("create", 0, false)
	if err != nil {
		return MultipartUpload{}, err
	}
	defer s.releaseAdmissionLocked(admitted)
	u := MultipartUpload{Version: 1, ID: id, ReceiptID: receiptID, Bucket: bucket, Generation: b.Generation, Key: key, Status: multipartActive, CreatedAt: time.Now().UTC(), Headers: headers, Checksum: options.Checksum, MaxBytes: options.MaxBytes}
	u.ExpiresAt = u.CreatedAt.Add(api.ObjectMultipartUploadTTL)
	if err = os.MkdirAll(filepath.Join(s.multipartPath(id), "parts"), 0o700); err != nil {
		return MultipartUpload{}, err
	}
	if err = syncDirectory(s.multipartPath(id)); err != nil {
		return MultipartUpload{}, err
	}
	if err = syncDirectory(filepath.Dir(s.multipartPath(id))); err != nil {
		return MultipartUpload{}, err
	}
	if err = s.writeMultipart(u); err != nil {
		return MultipartUpload{}, err
	}
	return u, nil
}

func (s *Store) activeMultipartCount(bucket, generation string) (int, error) {
	return s.indexedActiveMultipartCount(bucket, generation)
}

func (s *Store) AbortMultipart(bucket, key, id string) error {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return err
	}
	u, err := s.upload(bucket, key, id)
	if err != nil {
		return err
	}
	if s.assembling[id] {
		return ErrUploadBusy
	}
	if terminal, err := s.settleMultipart(&u); err != nil {
		return err
	} else if terminal {
		return ErrNoUpload
	}
	if u.Status == multipartAborted || u.Status == multipartExpired {
		return s.cleanMultipartParts(u.ID)
	}
	u.Status = multipartAborted
	if err = s.writeMultipart(u); err != nil {
		return err
	}
	return s.cleanMultipartParts(u.ID)
}

func (s *Store) cleanMultipartParts(id string) error {
	path := filepath.Join(s.multipartPath(id), "parts")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if s.replication != nil {
		_, err := s.journalRemove(filepath.ToSlash(filepath.Join("multipart", id, "parts")))
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if err := syncDirectory(s.multipartPath(id)); err != nil {
		return err
	}
	s.observeCatalogPath(path, true)
	return nil
}
