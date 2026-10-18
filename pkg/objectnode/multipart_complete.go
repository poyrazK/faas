package objectnode

import (
	"context"
	"crypto/md5" // #nosec G501 -- S3 multipart ETag compatibility.
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

type MultipartCompletion struct {
	Parts                []objectmultipart.Part
	IfMatch, IfNoneMatch string
	Checksum             objectchecksum.Digest
	ExpectedSize         *int64
}

func validManifest(m objectmultipart.Manifest, policy objectchecksum.Policy, maxBytes int64) bool {
	if !validReceiptID(m.UploadID) || m.Size < 0 || m.Size > maxBytes || len(m.Parts) < 1 || len(m.Parts) > api.MaxMultipartParts || m.IfMatch != "" && m.IfNoneMatch != "" || m.IfNoneMatch != "" && m.IfNoneMatch != "*" || m.Checksum.Policy != policy || !m.Checksum.Valid(len(m.Parts)) {
		return false
	}
	var previous int32
	for _, p := range m.Parts {
		if p.PartNumber <= previous || p.PartNumber > api.MaxMultipartParts || len(p.ETag) != 34 {
			return false
		}
		previous = p.PartNumber
	}
	return true
}

func (s *Store) completionManifest(u MultipartUpload, request MultipartCompletion) (objectmultipart.Manifest, []MultipartPart, error) {
	m := objectmultipart.Manifest{UploadID: u.ID, Parts: slices.Clone(request.Parts), IfMatch: request.IfMatch, IfNoneMatch: request.IfNoneMatch}
	if len(m.Parts) < 1 || len(m.Parts) > api.MaxMultipartParts || m.IfMatch != "" && m.IfNoneMatch != "" || m.IfNoneMatch != "" && m.IfNoneMatch != "*" {
		return m, nil, ErrInvalid
	}
	selected := make([]MultipartPart, 0, len(m.Parts))
	checksums := make([]objectchecksum.Part, 0, len(m.Parts))
	var previous int32
	for _, p := range m.Parts {
		if p.PartNumber <= previous || p.PartNumber > api.MaxMultipartParts {
			return m, nil, ErrPartOrder
		}
		previous = p.PartNumber
	}
	for i, p := range m.Parts {
		part, err := s.readPart(u.ID, p.PartNumber)
		if err != nil {
			return m, nil, err
		}
		if p.ETag != part.Object.ETag || part.Object.Key != u.Key {
			return m, nil, ErrInvalidPart
		}
		if p.Checksum != "" && (u.Checksum.Empty() || p.Checksum != part.Object.Checksum) {
			return m, nil, ErrDigest
		}
		if !u.Checksum.Empty() {
			m.Parts[i].Checksum = part.Object.Checksum
		}
		if i < len(m.Parts)-1 && part.Object.Size < api.MinMultipartPartBytes {
			return m, nil, ErrPartSmall
		}
		if part.Object.Size > u.MaxBytes-m.Size {
			return m, nil, ErrCapacity
		}
		m.Size += part.Object.Size
		if err = syncDirectory(s.partPath(u.ID, p.PartNumber)); err != nil {
			return m, nil, err
		}
		selected = append(selected, part)
		if !u.Checksum.Empty() {
			if part.Object.ChecksumAlgorithm != u.Checksum.Algorithm {
				return m, nil, ErrCorrupt
			}
			checksums = append(checksums, objectchecksum.Part{Number: p.PartNumber, Size: part.Object.Size, Value: part.Object.Checksum})
		}
	}
	if !u.Checksum.Empty() {
		checksum, err := objectchecksum.Build(u.Checksum, checksums)
		if err != nil {
			return m, nil, ErrInvalidPart
		}
		m.Checksum = checksum.Digest
	}
	if request.ExpectedSize != nil && *request.ExpectedSize != m.Size || !request.Checksum.Empty() && request.Checksum != m.Checksum {
		return m, nil, ErrDigest
	}
	return m, selected, nil
}

func completionMatches(m objectmultipart.Manifest, r MultipartCompletion) bool {
	if len(m.Parts) != len(r.Parts) || m.IfMatch != r.IfMatch || m.IfNoneMatch != r.IfNoneMatch || r.ExpectedSize != nil && m.Size != *r.ExpectedSize || !r.Checksum.Empty() && m.Checksum != r.Checksum {
		return false
	}
	for i, p := range r.Parts {
		if p.PartNumber != m.Parts[i].PartNumber || p.ETag != m.Parts[i].ETag || p.Checksum != "" && p.Checksum != m.Parts[i].Checksum {
			return false
		}
	}
	return true
}

// prepareMultipart freezes the manifest while holding the catalog lock. Parts
// cannot change after this point; assembly streams without holding that lock.
func (s *Store) prepareMultipart(bucket, key, id string, request MultipartCompletion) (MultipartUpload, []MultipartPart, *Object, *admissionCost, error) {
	if err := s.finishPending(false); err != nil {
		return MultipartUpload{}, nil, nil, nil, err
	}
	u, err := s.upload(bucket, key, id)
	if err != nil {
		return u, nil, nil, nil, err
	}
	if u.Completion != nil && !completionMatches(*u.Completion, request) {
		return u, nil, nil, nil, ErrReceiptReuse
	}
	terminal, err := s.settleMultipart(&u)
	if err != nil {
		return u, nil, nil, nil, err
	}
	if terminal {
		r, err := s.readReceipt(u.ReceiptID)
		if err != nil {
			return u, nil, nil, nil, err
		}
		if r.Status == ReceiptRejected {
			return u, nil, nil, nil, ErrPrecondition
		}
		return u, nil, &r.Object, nil, nil
	}
	if u.Status != multipartActive && u.Status != multipartCompleting || !time.Now().Before(u.ExpiresAt) {
		return u, nil, nil, nil, ErrNoUpload
	}
	if s.assembling[id] {
		return u, nil, nil, nil, ErrUploadBusy
	}
	m, parts, err := s.completionManifest(u, request)
	if err != nil {
		return u, nil, nil, nil, err
	}
	if u.Completion != nil && u.Completion.Hash() != m.Hash() {
		return u, nil, nil, nil, ErrCorrupt
	}
	admitted, err := s.admitWriteLocked("complete", m.Size, u.Completion != nil)
	if err != nil {
		return u, nil, nil, nil, err
	}
	u.Status, u.Completion = multipartCompleting, &m
	if err = s.writeMultipart(u); err != nil {
		s.releaseAdmissionLocked(admitted)
		return u, nil, nil, nil, err
	}
	s.assembling[id] = true
	return u, parts, nil, admitted, nil
}

// CompleteMultipart never publishes partial assembly bytes. The reserved space
// callback executes before assembly and its release executes on every outcome.
func (s *Store) CompleteMultipart(ctx context.Context, bucket, key, id string, request MultipartCompletion, reserve func(int64) (func(), error)) (Object, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return Object{}, ErrClosed
	}
	s.mu.Lock()
	u, parts, replay, admitted, err := s.prepareMultipart(bucket, key, id, request)
	s.mu.Unlock()
	defer s.releaseAdmission(admitted)
	if err != nil {
		return Object{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	defer func() { s.mu.Lock(); delete(s.assembling, id); s.mu.Unlock() }()
	if reserve != nil {
		release, err := reserve(u.Completion.Size)
		if err != nil {
			return Object{}, err
		}
		defer release()
	}
	stageID := uuid.NewString()
	stage := filepath.Join(s.root, "staging", stageID)
	defer func() { _ = os.Remove(stage) }()
	body := &multipartReader{store: s, upload: id, parts: parts}
	defer func() { _ = body.Close() }()
	o, err := stageObject(ctx, stage, key, stageID, body, u.Completion.Size, PutOptions{Headers: u.Headers})
	if err != nil {
		return Object{}, err
	}
	o.Modified = time.Now().UTC()
	o.ETag, o.MultipartParts = multipartETag(parts), len(parts)
	o.ChecksumAlgorithm, o.Checksum, o.ChecksumType = u.Completion.Checksum.Algorithm, u.Completion.Checksum.Value, u.Completion.Checksum.Type
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
	if err = ctx.Err(); err != nil {
		return Object{}, err
	}
	if _, err = s.upload(bucket, key, id); err != nil {
		return Object{}, err
	}
	r := writeReceipt{Version: 1, ID: u.ReceiptID, Bucket: bucket, Generation: u.Generation, Kind: "multipart", ManifestHash: u.Completion.Hash(), Status: receiptCommitting, IfMatch: request.IfMatch, IfNoneMatch: request.IfNoneMatch, Object: o}
	o, err = s.publishRecorded(stage, r, PutOptions{IfMatch: request.IfMatch, IfNoneMatch: request.IfNoneMatch})
	if err != nil && !errors.Is(err, ErrPrecondition) {
		return Object{}, err
	}
	if _, settleErr := s.settleMultipart(&u); settleErr != nil {
		return Object{}, settleErr
	}
	return o, err
}

func multipartETag(parts []MultipartPart) string {
	h := md5.New() // #nosec G401 -- S3 multipart ETag is MD5 of binary part MD5s.
	for _, part := range parts {
		digest, _ := hex.DecodeString(strings.Trim(part.Object.ETag, `"`))
		_, _ = h.Write(digest)
	}
	return `"` + hex.EncodeToString(h.Sum(nil)) + `-` + strconv.Itoa(len(parts)) + `"`
}

func (s *Store) settleMultipart(u *MultipartUpload) (bool, error) {
	terminal, err := s.settleMultipartStatus(u)
	if err != nil || !terminal {
		return terminal, err
	}
	if err = s.cleanMultipartParts(u.ID); err != nil {
		return false, err
	}
	return true, nil
}

// Expiry shares receipt validation but reclaims parts in separate bounded turns.
func (s *Store) settleMultipartStatus(u *MultipartUpload) (bool, error) {
	r, err := s.readReceipt(u.ReceiptID)
	if errors.Is(err, ErrNoReceipt) {
		if u.Status == multipartCompleted || u.Status == multipartRejected {
			return false, ErrCorrupt
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if r.Kind != "multipart" || r.Bucket != u.Bucket || r.Generation != u.Generation || r.Object.Key != u.Key || u.Completion == nil || r.ManifestHash != u.Completion.Hash() || r.Status == receiptCommitting || u.Status == multipartAborted || u.Status == multipartExpired {
		return false, ErrCorrupt
	}
	status := multipartCompleted
	if r.Status == ReceiptRejected {
		status = multipartRejected
	}
	if u.Status != status {
		u.Status = status
		if err = s.writeMultipart(*u); err != nil {
			return false, err
		}
	}
	return true, nil
}
