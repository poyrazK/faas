package main

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) mintObjectUploadGrant(ctx context.Context, g state.ObjectUploadGrant, expires int64) (objectstorage.SignedRequest, error) {
	st, ok := s.store.(state.ObjectUploadGrantStore)
	if !ok || s.objectStorage == nil {
		return objectstorage.SignedRequest{}, objectstorage.ErrUnavailable
	}
	if expires == 0 {
		expires = api.DefaultObjectSignedURLExpiresSeconds
	}
	if expires < 1 || expires > api.MaxObjectSignedURLExpiresSeconds {
		return objectstorage.SignedRequest{}, objectstorage.ErrInvalid
	}
	token, hash, err := s3gateway.GenerateUploadGrantToken()
	if err != nil {
		return objectstorage.SignedRequest{}, objectstorage.ErrUnavailable
	}
	url, err := s3gateway.UploadGrantURL(s.objectStorage.PublicEndpoint, g.Bucket.Name, g.Key, token)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	g.ID, g.TokenHash = uuid.NewString(), hash
	created, err := st.CreateObjectUploadGrant(ctx, g, int(expires))
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	return objectstorage.SignedRequest{URL: url, Method: http.MethodPut, Headers: created.Headers, ExpiresAt: created.ExpiresAt}, nil
}

func (s *server) mintObjectPutGrant(ctx context.Context, b state.ObjectBucket, req objectstorage.SignRequest) (objectstorage.SignedRequest, error) {
	if err := req.Validate(min(s.objectStorage.MaxUploadBytes, api.MaxObjectSinglePutBytes)); err != nil {
		return objectstorage.SignedRequest{}, err
	}
	headers, err := objectPutGrantHeaders(req)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	return s.mintObjectUploadGrant(ctx, state.ObjectUploadGrant{Bucket: b, Kind: state.ObjectUploadGrantPut, Key: req.Key, SizeBytes: *req.SizeBytes, Headers: headers}, req.ExpiresIn)
}

func objectPutGrantHeaders(req objectstorage.SignRequest) (map[string]string, error) {
	headers := map[string]string{"Content-Length": strconv.FormatInt(*req.SizeBytes, 10), "Content-Type": req.ContentType}
	if headers["Content-Type"] == "" {
		headers["Content-Type"] = "application/octet-stream"
	}
	for name, value := range map[string]string{"Cache-Control": req.CacheControl, "Content-Disposition": req.ContentDisposition, "Content-Encoding": req.ContentEncoding, "Content-Language": req.ContentLanguage} {
		if value != "" {
			headers[name] = value
		}
	}
	for name, value := range req.Metadata {
		headers[http.CanonicalHeaderKey("X-Amz-Meta-"+name)] = value
	}
	tags, err := objectstorage.EncodeObjectTags(req.Tags)
	if err != nil {
		return nil, err
	}
	if tags != "" {
		headers["X-Amz-Tagging"] = tags
	}
	return headers, nil
}

func (s *server) mintObjectPartGrant(ctx context.Context, b state.ObjectBucket, u state.ObjectMultipartUpload, part objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	return s.mintObjectUploadGrant(ctx, state.ObjectUploadGrant{Bucket: b, Kind: state.ObjectUploadGrantMultipartPart, Key: u.Key,
		SizeBytes: part.SizeBytes, Headers: map[string]string{"Content-Length": strconv.FormatInt(part.SizeBytes, 10)},
		UploadID: u.ID, ProviderUploadID: u.ProviderUploadID, PartNumber: part.PartNumber}, part.ExpiresIn)
}

func (s *server) pruneObjectUploadGrants(ctx context.Context) error {
	st, ok := s.store.(state.ObjectUploadGrantStore)
	if !ok {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, api.ObjectMutationObservationTimeout)
	defer cancel()
	_, err := st.PruneExpiredObjectUploadGrants(callCtx, api.ObjectUploadGrantPruneBatch)
	return err
}
