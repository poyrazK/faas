package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueObjectMultipartPartURL(r *http.Request, b state.ObjectBucket, u state.ObjectMultipartUpload, req objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	store, ok := s.store.(state.ObjectMultipartURLCapabilityStore)
	if !ok {
		return objectstorage.SignedRequest{}, objectstorage.ErrConfiguration
	}
	size := req.SizeBytes
	sign, err := objectstorage.NormalizePublicSignRequest(objectstorage.SignRequest{Method: http.MethodPut, Key: u.Key, SizeBytes: &size, ExpiresIn: req.ExpiresIn}, s.objectStorage.MaxPartBytes)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	sign.ExpiresIn = min(sign.ExpiresIn, int64(time.Until(u.ExpiresAt)/time.Second))
	if sign.ExpiresIn < 1 {
		return objectstorage.SignedRequest{}, state.ErrConflict
	}
	c, _, secret, err := s.prepareObjectURLCredential(r, b, sign, state.ObjectBucketPermissionWrite)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	c.URL.ReceiptID = ""
	c.URL.Multipart = &state.ObjectURLMultipartPart{UploadID: u.ID, PartNumber: req.PartNumber}
	if c.URL.ExpiresAt.After(u.ExpiresAt) {
		return objectstorage.SignedRequest{}, state.ErrConflict
	}
	now := c.URL.ExpiresAt.Add(-time.Duration(c.URL.Request.ExpiresIn) * time.Second)
	out, err := objectstorage.PresignPublicMultipartPart(r.Context(), s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, b.Name, c.AccessKeyID, secret, objectstorage.SignRequest(c.URL.Request), u.ID, req.PartNumber, now)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	if _, err = store.IssueObjectMultipartURLCredential(r.Context(), c, u, s.objectStorage.Accounting); err != nil {
		return objectstorage.SignedRequest{}, err
	}
	return out, nil
}
