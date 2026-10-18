package objectnode

import (
	"context"
	"crypto/md5" // #nosec G501 -- S3 ETag and Content-MD5 compatibility, not authentication.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

// Object is an immutable blob identity plus the current catalog entry. IDs and
// physical paths are internal and are not S3 version IDs.
type Object struct {
	Version           int         `json:"version"`
	ID                string      `json:"id"`
	Key               string      `json:"key"`
	Size              int64       `json:"size"`
	ETag              string      `json:"etag"`
	SHA256            string      `json:"sha256"`
	Modified          time.Time   `json:"modified"`
	Headers           http.Header `json:"headers"`
	ChecksumAlgorithm string      `json:"checksum_algorithm,omitempty"`
	Checksum          string      `json:"checksum,omitempty"`
	ChecksumType      string      `json:"checksum_type,omitempty"`
	MultipartParts    int         `json:"multipart_parts,omitempty"`
}

type PutOptions struct {
	Receipt           string
	Headers           http.Header
	IfMatch           string
	IfNoneMatch       string
	ContentMD5        string
	PayloadSHA256     string
	ChecksumAlgorithm string
	Checksum          string
}

func validKey(key string) bool {
	if key == "" || len(key) > api.MaxObjectS3ListTextBytes || !utf8.ValidString(key) {
		return false
	}
	return !strings.ContainsFunc(key, func(c rune) bool { return c < 32 || c == 127 })
}

func readObject(dir string) (Object, error) {
	var o Object
	err := readJSON(filepath.Join(dir, "current.json"), &o)
	if errors.Is(err, os.ErrNotExist) {
		return o, ErrNoObject
	}
	if err != nil || o.Version != 1 || uuid.Validate(o.ID) != nil || !validKey(o.Key) || nameHash(o.Key) != filepath.Base(dir) || o.Size < 0 || len(o.SHA256) != 64 || !validObjectETag(o) || o.Modified.IsZero() {
		return o, ErrCorrupt
	}
	return o, nil
}

func validObjectETag(o Object) bool {
	if o.MultipartParts == 0 {
		return len(o.ETag) == 34
	}
	return o.MultipartParts > 0 && o.MultipartParts <= api.MaxMultipartParts && len(o.ETag) > 35 && strings.HasPrefix(o.ETag, `"`) && strings.HasSuffix(o.ETag, `-`+strconv.Itoa(o.MultipartParts)+`"`)
}

func (s *Store) Get(bucket, key string) (Object, *os.File, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Object{}, nil, ErrClosed
	}
	if s.pending != nil {
		return Object{}, nil, ErrCorrupt
	}
	if _, err := s.bucket(bucket); err != nil {
		return Object{}, nil, err
	}
	o, err := readObject(s.objectPath(bucket, key))
	if err != nil {
		return o, nil, err
	}
	f, err := os.Open(filepath.Join(s.objectPath(bucket, key), o.ID+".data")) //nolint:forbidigo // Validated private immutable blob identity.
	if err != nil {
		return o, nil, ErrCorrupt
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != o.Size {
		_ = f.Close()
		return o, nil, ErrCorrupt
	}
	return o, f, nil
}

func (s *Store) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, options PutOptions) (Object, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return Object{}, ErrClosed
	}
	if !validKey(key) || size < 0 || size > api.MaxObjectSinglePutBytes || options.IfMatch != "" && options.IfNoneMatch != "" || options.IfNoneMatch != "" && options.IfNoneMatch != "*" {
		return Object{}, ErrInvalid
	}
	var err error
	options, err = normalizeReceiptOptions(options)
	if err != nil {
		return Object{}, err
	}
	s.mu.RLock()
	b, err := s.bucket(bucket)
	s.mu.RUnlock()
	if err != nil {
		return Object{}, err
	}
	admitted, err := s.beginAdmission("put", size, options.Receipt)
	if err != nil {
		return Object{}, err
	}
	defer s.releaseAdmission(admitted)
	id := uuid.NewString()
	stage := filepath.Join(s.root, "staging", id)
	defer func() { _ = os.Remove(stage) }()
	o, err := stageObject(ctx, stage, key, id, body, size, options)
	if err != nil {
		return Object{}, err
	}
	prepared, err := s.prepareReplay(ctx, stage, o)
	if err != nil {
		return Object{}, err
	}
	defer prepared.close()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.bindPreparedReplay(prepared)()
	if err = s.finishPending(false); err != nil {
		return Object{}, err
	}
	current, err := s.bucket(bucket)
	if err != nil {
		return Object{}, err
	}
	if current.Generation != b.Generation {
		return Object{}, ErrNoBucket
	}
	if err = ctx.Err(); err != nil {
		return Object{}, err
	}
	o.Modified = time.Now().UTC()
	return s.publish(stage, bucket, o, options)
}

func stageObject(ctx context.Context, path, key, id string, body io.Reader, size int64, options PutOptions) (Object, error) {
	o := Object{Version: 1, ID: id, Key: key, Size: size, Headers: options.Headers.Clone(), ChecksumAlgorithm: options.ChecksumAlgorithm, Checksum: options.Checksum}
	md5Hash := md5.New() // #nosec G401 -- S3 single-PUT ETag is a protocol checksum.
	shaHash := sha256.New()
	writers := []io.Writer{md5Hash, shaHash}
	var additional hash.Hash
	if options.ChecksumAlgorithm != "" {
		additional = objectchecksum.NewHash(options.ChecksumAlgorithm)
		if additional == nil || !objectchecksum.ValidPart(options.ChecksumAlgorithm, options.Checksum) {
			return o, ErrInvalid
		}
		writers = append(writers, additional)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return o, err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(io.MultiWriter(append(writers, f)...), io.LimitReader(contextReader{ctx, body}, size+1))
	if err != nil {
		return o, err
	}
	if n != size {
		return o, ErrInvalid
	}
	o.ETag, o.SHA256 = `"`+hex.EncodeToString(md5Hash.Sum(nil))+`"`, hex.EncodeToString(shaHash.Sum(nil))
	if options.ContentMD5 != "" && options.ContentMD5 != base64.StdEncoding.EncodeToString(md5Hash.Sum(nil)) || options.PayloadSHA256 != "" && options.PayloadSHA256 != o.SHA256 || additional != nil && options.Checksum != base64.StdEncoding.EncodeToString(additional.Sum(nil)) {
		return o, ErrDigest
	}
	if err = f.Sync(); err != nil {
		return o, err
	}
	return o, f.Close()
}

func (s *Store) publish(stage, bucket string, o Object, options PutOptions) (Object, error) {
	if options.Receipt != "" {
		return s.publishTracked(stage, bucket, o, options)
	}
	dir := s.objectPath(bucket, o.Key)
	previous, err := readObject(dir)
	if err != nil && !errors.Is(err, ErrNoObject) {
		return Object{}, err
	}
	exists := err == nil
	if options.IfNoneMatch == "*" && exists || options.IfMatch != "" && (!exists || options.IfMatch != "*" && options.IfMatch != previous.ETag) {
		return Object{}, ErrPrecondition
	}
	if err = os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return Object{}, err
	}
	if err = syncDirectory(filepath.Dir(dir)); err != nil {
		return Object{}, err
	}
	blob := filepath.Join(dir, o.ID+".data")
	if err = s.moveStagedBlob(stage, blob); err != nil {
		return Object{}, err
	}
	if err = syncDirectory(dir); err != nil {
		return Object{}, err
	}
	published, err := s.writeRecord(filepath.Join(dir, "current.json"), o)
	if err != nil {
		if !published {
			_ = os.Remove(blob)
		}
		return Object{}, err
	}
	if exists {
		_ = os.Remove(filepath.Join(dir, previous.ID+".data"))
	}
	return o, nil
}

func (s *Store) Delete(bucket, key string) error {
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
	if _, err := s.bucket(bucket); err != nil {
		return err
	}
	dir := s.objectPath(bucket, key)
	o, err := readObject(dir)
	if errors.Is(err, ErrNoObject) {
		return nil
	}
	if err != nil {
		return err
	}
	s.invalidateInventory()
	if s.replication != nil {
		_, err = s.journalRemove(filepath.ToSlash(filepath.Join("buckets", nameHash(bucket), "objects", nameHash(key))))
		return err
	}
	if err = os.Remove(filepath.Join(dir, "current.json")); err != nil {
		return err
	}
	if err = syncDirectory(dir); err != nil {
		s.failCatalogIndex()
		return err
	}
	s.observeCatalogPath(dir, true)
	_ = os.Remove(filepath.Join(dir, o.ID+".data"))
	return nil
}

type contextReader struct {
	ctx  context.Context
	body io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}
