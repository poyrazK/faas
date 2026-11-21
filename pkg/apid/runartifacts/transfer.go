package runartifacts

import (
	"context"
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 compatibility.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) transfer(ctx context.Context, access Access, routeName string, route state.ObjectUploadRoute, bucket state.ObjectBucket, req api.DeliverRunArtifactRequest, key, fingerprint string, open OpenSource) (api.RunArtifactDelivery, error) {
	if s.Slots == nil || open == nil {
		return api.RunArtifactDelivery{}, api.ErrCapacity("artifact delivery transfers are unavailable")
	}
	select {
	case s.Slots <- struct{}{}:
		defer func() { <-s.Slots }()
	default:
		return api.RunArtifactDelivery{}, api.ErrCapacity("artifact delivery staging is busy; retry the same request")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ExecutionFileUploadTimeout)
	defer cancel()
	body, err := open(ctx, req)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	defer body.Close() //nolint:errcheck
	file, checksum, err := s.stage(ctx, req, body)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	defer file.Close() //nolint:errcheck
	c := state.ObjectUploadCompletion{ID: objectstorage.NewWriteReceiptID(), AccountID: access.AccountID, AppID: access.AppID, RouteID: route.ID, BucketID: bucket.ID,
		SubjectID: subject(access.AppID), Key: objectKey(route, key, req.ArtifactName), Bytes: req.SizeBytes, ContentType: req.ContentType, Status: "pending",
		IdempotencyKey: key, RequestFingerprint: fingerprint, Encryption: route.Encryption.Clone(), RuntimeSinglePutLimit: s.Registry.MaxSinglePutBytes}
	ctx = objectstorage.WithObjectWriteChecksum(ctx, checksum)
	c, err = objectstorage.DeliverRouteObject(ctx, s.Registry, s.Store, bucket, c, file)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	return s.viewAuthorized(ctx, access, routeName, c)
}

// Unlink before reading plaintext: crashes cannot leave customer data in /tmp.
// Each slot holds at most ExecutionFileHardMaxBytes and one streaming buffer.
func (s Service) stage(ctx context.Context, req api.DeliverRunArtifactRequest, body io.ReadCloser) (*os.File, string, error) {
	file, err := os.CreateTemp(s.TempDir, "gregale-run-delivery-*")
	if err != nil {
		return nil, "", api.ErrCapacity("artifact staging is unavailable")
	}
	if err = os.Remove(file.Name()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, "", api.ErrCapacity("private artifact staging is unavailable")
	}
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stop()
	sha := sha256.New()
	md5sum := md5.New() // #nosec G401 -- S3 Content-MD5 compatibility.
	n, copyErr := io.CopyBuffer(io.MultiWriter(file, sha, md5sum), io.LimitReader(body, req.SizeBytes+1), make([]byte, api.ExecutionFileStreamChunkBytes))
	if copyErr != nil || ctx.Err() != nil || n != req.SizeBytes || "sha256:"+hex.EncodeToString(sha.Sum(nil)) != req.SHA256 {
		_ = file.Close()
		return nil, "", api.NewProblem(422, "run_artifact_integrity", "Artifact could not be verified", "source transfer failed or does not match the selected output")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, "", api.ErrCapacity("artifact staging is unavailable")
	}
	return file, base64.StdEncoding.EncodeToString(md5sum.Sum(nil)), nil
}
