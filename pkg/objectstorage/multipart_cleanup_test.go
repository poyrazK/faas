package objectstorage

import (
	"context"
	"errors"
	"testing"
)

type cleanupTestProvider struct {
	Provider
	page MultipartPartsPage
	err  error
}

func (p cleanupTestProvider) ListMultipartParts(_ context.Context, _ string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	if r.Key != "key" || r.ProviderUploadID != "upload" || r.Limit != 1 {
		return MultipartPartsPage{}, ErrInvalid
	}
	return p.page, p.err
}

func TestVerifyMultipartAbort(t *testing.T) {
	for _, tc := range []struct {
		name      string
		page      MultipartPartsPage
		err, want error
	}{
		{name: "empty"},
		{name: "absent", err: ErrNotFound},
		{name: "late part", page: MultipartPartsPage{Items: []MultipartPart{{PartNumber: 1}}}, want: ErrConflict},
		{name: "truncated empty", page: MultipartPartsPage{NextPartNumberMarker: 1}, want: ErrConflict},
		{name: "permissions", err: ErrConfiguration, want: ErrConfiguration},
		{name: "temporary", err: ErrUnavailable, want: ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyMultipartAbort(t.Context(), cleanupTestProvider{page: tc.page, err: tc.err}, "bucket", MultipartAbortRequest{Key: "key", ProviderUploadID: "upload"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v, want %v", err, tc.want)
			}
		})
	}
}
