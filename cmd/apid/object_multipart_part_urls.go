package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueObjectMultipartPartURL(ctx context.Context, b state.ObjectBucket, u state.ObjectMultipartUpload, p objectstorage.Provider, req objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	fence, ok := s.store.(state.ObjectMultipartPartURLStore)
	if !ok {
		return objectstorage.SignedRequest{}, objectstorage.ErrConfiguration
	}
	if err := s.admitObjectMultipartPartURL(ctx, b, u.Key); err != nil {
		return objectstorage.SignedRequest{}, err
	}
	out, err := p.PresignMultipartPart(ctx, b.PhysicalName, req)
	if err != nil {
		return objectstorage.SignedRequest{}, err
	}
	if err = fence.RecordObjectMultipartPartURL(ctx, u, out.ExpiresAt); err != nil {
		return objectstorage.SignedRequest{}, err
	}
	return out, nil
}
