package object

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// CheckPrivateBackupBucket validates the current placement before the caller
// queues a dump. Each upload uses the bucket's current protection and quota.
func (s *Service) CheckPrivateBackupBucket(b state.ObjectBucket) error {
	if !s.ObjectStorageEnabled() {
		return ErrObjectStorageDisabled
	}
	if b.State != "ready" || b.PublicRead || b.ServeAt != "" {
		return objectstorage.ErrInvalid
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return err
	}
	return objectstorage.ValidateConditionalPut(backend.Provider, objectstorage.ObjectWriteConditions{IfNoneMatch: "*"})
}

// PutImmutableBackupObject streams an archive or manifest through Object
// Storage's signed gateway write path. Its create-only condition makes replay
// safe even if a provider committed a previous request whose response was lost.
func (s *Service) PutImmutableBackupObject(ctx context.Context, b state.ObjectBucket, key, mediaType, digest string, size int64, body io.Reader) error {
	if err := s.CheckPrivateBackupBucket(b); err != nil {
		return err
	}
	if size <= 0 || !strings.HasPrefix(key, "postgres-backups/") {
		return objectstorage.ErrInvalid
	}
	if limit := s.Host.ObjectStorage().MaxSinglePutBytes; size > limit {
		return &state.ObjectStorageLimitError{Kind: "backup_single_put_bytes", Limit: limit, Observed: size, Cause: objectstorage.ErrInvalid}
	}
	reader := &verifiedBackupBody{body: body, digest: digest, remaining: size, sum: sha256.New()}
	req := objectstorage.SignRequest{
		Method: http.MethodPut, Key: key, ContentType: mediaType, SizeBytes: &size,
		IfNoneMatch: "*", Metadata: map[string]string{"sha256": digest},
	}
	err := s.putBackupObject(ctx, b, req, reader)
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && s.verifyExistingBackupObject(ctx, b, key, digest, size) == nil {
		return nil
	}
	return err
}

var errBackupIntegrity = errors.New("PostgreSQL backup object does not match its manifest")

type verifiedBackupBody struct {
	body      io.Reader
	digest    string
	remaining int64
	sum       hash.Hash
	verified  atomic.Bool
}

func (r *verifiedBackupBody) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n, err := r.body.Read(p[:min(int64(len(p)), r.remaining)])
	r.remaining -= int64(n)
	_, _ = r.sum.Write(p[:n])
	if r.remaining != 0 {
		return n, err
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	// Do not send a complete request body unless the verified length and
	// digest match. Reading one extra byte also checks the archive footer.
	var extra [1]byte
	if _, end := io.ReadFull(r.body, extra[:]); end != io.EOF || hex.EncodeToString(r.sum.Sum(nil)) != r.digest {
		return 0, errBackupIntegrity
	}
	r.verified.Store(true)
	return n, nil
}

func (s *Service) putBackupObject(ctx context.Context, b state.ObjectBucket, sign objectstorage.SignRequest, body *verifiedBackupBody) error {
	resp, err := s.backupObjectRequest(ctx, b, sign, state.ObjectBucketPermissionWrite, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return objectstorage.ErrUnavailable
	}
	if !body.verified.Load() {
		return errBackupIntegrity
	}
	return nil
}

func (s *Service) verifyExistingBackupObject(ctx context.Context, b state.ObjectBucket, key, digest string, size int64) error {
	sign := objectstorage.SignRequest{Method: http.MethodGet, Key: key}
	resp, err := s.backupObjectRequest(ctx, b, sign, state.ObjectBucketPermissionRead, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK || resp.ContentLength >= 0 && resp.ContentLength != size {
		return objectstorage.ErrNotFound
	}
	sum := sha256.New()
	read, err := io.Copy(sum, io.LimitReader(resp.Body, size+1))
	if err != nil || read != size || hex.EncodeToString(sum.Sum(nil)) != digest {
		return errBackupIntegrity
	}
	return nil
}

func (s *Service) backupObjectRequest(ctx context.Context, b state.ObjectBucket, sign objectstorage.SignRequest, permission string, body io.Reader) (*http.Response, error) {
	capability, err := s.issueBucketObjectCapability((&http.Request{}).WithContext(ctx), b, sign, permission)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, sign.Method, capability.URL, body)
	if err != nil {
		return nil, objectstorage.ErrConfiguration
	}
	for name, value := range capability.Headers {
		req.Header.Set(name, value)
	}
	if sign.SizeBytes != nil {
		req.ContentLength = *sign.SizeBytes
	}
	req.Header.Set("Accept-Encoding", "identity")
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors can include the signed URL.
		return nil, objectstorage.ErrUnavailable
	}
	return resp, nil
}
