package objectnode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const ReceiptMetadataHeader = "X-Amz-Meta-Gregale-Upload-Receipt"

const (
	ReceiptCommitted  = "committed"
	ReceiptRejected   = "rejected"
	receiptCommitting = "committing"
)

// WriteReceipt retains outcome metadata independently of current object bytes.
// It is private recovery evidence, not an S3 object version or a backup.
type WriteReceipt struct {
	ID           string
	Status       string
	Object       Object
	ManifestHash string
	SettledAt    *time.Time
}

type writeReceipt struct {
	Version      int        `json:"version"`
	ID           string     `json:"id"`
	Bucket       string     `json:"bucket"`
	Generation   string     `json:"generation"`
	RequestHash  string     `json:"request_hash"`
	Seal         string     `json:"seal"`
	Status       string     `json:"status"`
	PreviousID   string     `json:"previous_id,omitempty"`
	IfMatch      string     `json:"if_match,omitempty"`
	IfNoneMatch  string     `json:"if_none_match,omitempty"`
	Object       Object     `json:"object"`
	Kind         string     `json:"kind,omitempty"`
	ManifestHash string     `json:"manifest_hash,omitempty"`
	SettledAt    *time.Time `json:"settled_at,omitempty"`
}

func validReceiptID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func normalizeReceiptOptions(o PutOptions) (PutOptions, error) {
	if o.Receipt == "" {
		return o, nil
	}
	if !validReceiptID(o.Receipt) {
		return o, ErrInvalid
	}
	headers := make(http.Header, len(o.Headers)+1)
	for name, values := range o.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := headers[canonical]; duplicate {
			return o, ErrInvalid
		}
		headers[canonical] = append([]string(nil), values...)
	}
	if marker := headers.Get(ReceiptMetadataHeader); marker != "" && marker != o.Receipt {
		return o, ErrInvalid
	}
	headers.Set(ReceiptMetadataHeader, o.Receipt)
	o.Headers = headers
	return o, nil
}

func receiptRequestHash(r writeReceipt) string {
	if r.Kind == "multipart" {
		body, _ := json.Marshal(struct {
			Bucket, Generation, Manifest string
			Object                       Object
		}{r.Bucket, r.Generation, r.ManifestHash, r.Object})
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:])
	}
	request := struct {
		Bucket, Generation, Key, SHA256, IfMatch, IfNoneMatch string
		Size                                                  int64
		Headers                                               http.Header
		ChecksumAlgorithm, Checksum                           string
	}{r.Bucket, r.Generation, r.Object.Key, r.Object.SHA256, r.IfMatch, r.IfNoneMatch, r.Object.Size, r.Object.Headers, r.Object.ChecksumAlgorithm, r.Object.Checksum}
	body, _ := json.Marshal(request) // This fixed DTO has no unsupported JSON types.
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *Store) receiptPath(id string) string {
	return filepath.Join(s.root, "receipts", id+".json")
}

func (s *Store) readReceipt(id string) (writeReceipt, error) {
	var r writeReceipt
	err := readJSON(s.receiptPath(id), &r)
	if errors.Is(err, os.ErrNotExist) {
		return r, ErrNoReceipt
	}
	marker := ReceiptMetadataHeader
	if r.Kind == "multipart" {
		marker = MultipartMetadataHeader
	}
	if err != nil || r.Version != 1 || r.ID != id || !validReceiptID(id) || !ValidBucket(r.Bucket) || !validReceiptID(r.Generation) || !validReceiptID(r.Object.ID) || r.Object.Version != 1 || !validKey(r.Object.Key) || r.Object.Size < 0 || r.Object.Modified.IsZero() || r.Object.Headers.Get(marker) != id || r.RequestHash != receiptRequestHash(r) || r.Seal != sealReceipt(r).Seal || r.Kind != "" && r.Kind != "multipart" || r.Kind == "multipart" && len(r.ManifestHash) != 64 {
		return r, ErrCorrupt
	}
	if r.Status != receiptCommitting && r.Status != ReceiptCommitted && r.Status != ReceiptRejected || r.PreviousID != "" && !validReceiptID(r.PreviousID) || !validReceiptSettlement(r) {
		return r, ErrCorrupt
	}
	sha, err := hex.DecodeString(r.Object.SHA256)
	if err != nil || len(sha) != sha256.Size || !validObjectETag(r.Object) {
		return r, ErrCorrupt
	}
	return r, nil
}

// Receipt returns an exact terminal outcome, including after object overwrite,
// object deletion, or bucket deletion. Missing proof never establishes failure.
func (s *Store) Receipt(bucket, key, id string) (WriteReceipt, error) {
	return s.receipt(bucket, key, id, "")
}

func (s *Store) MultipartReceipt(bucket, key, id string) (WriteReceipt, error) {
	return s.receipt(bucket, key, id, "multipart")
}

func (s *Store) receipt(bucket, key, id, kind string) (WriteReceipt, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return WriteReceipt{}, ErrClosed
	}
	if !validReceiptID(id) || !ValidBucket(bucket) || !validKey(key) {
		return WriteReceipt{}, ErrInvalid
	}
	if err := s.finishPending(false); err != nil {
		return WriteReceipt{}, err
	}
	r, err := s.readReceipt(id)
	if err != nil {
		return WriteReceipt{}, err
	}
	if r.Bucket != bucket || r.Object.Key != key || r.Kind != kind {
		return WriteReceipt{}, ErrNoReceipt
	}
	return WriteReceipt{ID: r.ID, Status: r.Status, Object: r.Object, ManifestHash: r.ManifestHash, SettledAt: r.SettledAt}, nil
}

func (s *Store) publishTracked(stage, bucket string, o Object, options PutOptions) (Object, error) {
	b, err := s.bucket(bucket)
	if err != nil {
		return Object{}, err
	}
	r := writeReceipt{Version: 1, ID: options.Receipt, Bucket: bucket, Generation: b.Generation, Status: receiptCommitting, IfMatch: options.IfMatch, IfNoneMatch: options.IfNoneMatch, Object: o}
	return s.publishRecorded(stage, r, options)
}

func (s *Store) publishRecorded(stage string, r writeReceipt, options PutOptions) (Object, error) {
	o, bucket := r.Object, r.Bucket
	r.RequestHash = receiptRequestHash(r)
	if original, err := s.readReceipt(r.ID); !errors.Is(err, ErrNoReceipt) {
		if err != nil {
			return Object{}, err
		}
		if original.RequestHash != r.RequestHash {
			return Object{}, ErrReceiptReuse
		}
		if original.Status == ReceiptRejected {
			return Object{}, ErrPrecondition
		}
		if original.Status != ReceiptCommitted {
			return Object{}, ErrCorrupt
		}
		return original.Object, nil // Replay never changes the current object.
	}
	if err := s.checkNewReceiptID(r.ID); err != nil {
		return Object{}, err
	}
	dir := s.objectPath(bucket, o.Key)
	previous, err := readObject(dir)
	if err != nil && !errors.Is(err, ErrNoObject) {
		return Object{}, err
	}
	if err == nil {
		r.PreviousID = previous.ID
	}
	if options.IfNoneMatch == "*" && err == nil || options.IfMatch != "" && (err != nil || options.IfMatch != "*" && options.IfMatch != previous.ETag) {
		r.Status = ReceiptRejected
		if err = s.beginReceipt(r); err != nil {
			return Object{}, err
		}
		return Object{}, ErrPrecondition
	}
	if err = s.installTrackedBlob(stage, dir, o.ID); err != nil {
		return Object{}, err
	}
	if err = s.beginReceipt(r); err != nil {
		return Object{}, err
	}
	return o, nil
}

func (s *Store) installTrackedBlob(stage, dir, id string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := syncDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := s.moveStagedBlob(stage, filepath.Join(dir, id+".data")); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func (s *Store) beginReceipt(r writeReceipt) error {
	r = sealReceipt(r)
	s.pending = &r
	published, err := s.writeRecord(s.receiptPath(r.ID), r)
	if err != nil {
		if !published {
			s.pending = nil // No commit decision exists; its orphan blob is reclaimable.
		}
		return err
	}
	return s.finishPending(false)
}

// finishPending runs under the catalog write lock before every later mutation.
// Once a commit intent is durable it must finish, even if its HTTP caller left.
func (s *Store) finishPending(verifyBytes bool) error {
	if err := s.finishReplicationPending(); err != nil {
		return err
	}
	if s.pending == nil {
		return nil
	}
	r := *s.pending
	if err := syncDirectory(filepath.Join(s.root, "receipts")); err != nil {
		return err
	}
	if r.Status != ReceiptRejected {
		if err := s.applyReceipt(r, verifyBytes); err != nil {
			return err
		}
		r.Status = ReceiptCommitted
	}
	r = sealReceipt(r)
	if _, err := s.writeRecord(s.receiptPath(r.ID), r); err != nil {
		return err
	}
	s.pending = nil
	if r.Status == ReceiptCommitted && r.PreviousID != "" {
		_ = os.Remove(filepath.Join(s.objectPath(r.Bucket, r.Object.Key), r.PreviousID+".data"))
	}
	return nil
}

func sealReceipt(r writeReceipt) writeReceipt {
	r.Seal = ""
	body, _ := json.Marshal(r)
	sum := sha256.Sum256(body)
	r.Seal = hex.EncodeToString(sum[:])
	return r
}

func (s *Store) applyReceipt(r writeReceipt, verifyBytes bool) error {
	b, err := s.bucket(r.Bucket)
	if err != nil || b.Generation != r.Generation {
		return ErrCorrupt
	}
	dir := s.objectPath(r.Bucket, r.Object.Key)
	current, err := readObject(dir)
	if err != nil && (!errors.Is(err, ErrNoObject) || r.PreviousID != "") || err == nil && current.ID != r.PreviousID && current.ID != r.Object.ID {
		return ErrCorrupt
	}
	if err = verifyReceiptBlob(filepath.Join(dir, r.Object.ID+".data"), r.Object, verifyBytes); err != nil {
		return err
	}
	_, err = s.writeRecord(filepath.Join(dir, "current.json"), r.Object)
	return err
}

func verifyReceiptBlob(path string, o Object, verifyBytes bool) error {
	f, err := os.Open(path) //nolint:forbidigo // Validated journal-owned immutable blob identity.
	if err != nil {
		return ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != o.Size {
		return ErrCorrupt
	}
	if verifyBytes {
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != o.SHA256 {
			return ErrCorrupt
		}
	}
	return nil
}

func (s *Store) recoverReceipts() error {
	dir := filepath.Join(s.root, "receipts")
	err := visitDirectory(dir, func(entry os.DirEntry) error {
		if strings.HasPrefix(entry.Name(), ".tmp-") && validReceiptID(strings.TrimPrefix(entry.Name(), ".tmp-")) {
			return os.Remove(filepath.Join(dir, entry.Name()))
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() != id+".json" || !validReceiptID(id) {
			return ErrCorrupt
		}
		r, err := s.readReceipt(id)
		if err != nil {
			return err
		}
		if r.Status == receiptCommitting {
			if s.pending != nil {
				return ErrCorrupt
			}
			s.pending = &r
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = syncDirectory(dir); err != nil {
		return err
	}
	return s.finishPending(true)
}
