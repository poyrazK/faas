package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestS3MultipartInitiationClaimsBeforeSingleCreate(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    error
	}{
		{"positive", `<InitiateMultipartUploadResult><UploadId>original</UploadId></InitiateMultipartUploadResult>`, http.StatusOK, nil},
		{"uncertain", `<Error><Code>SlowDown</Code></Error>`, http.StatusServiceUnavailable, ErrUnavailable},
		{"invalid reply", `<InitiateMultipartUploadResult/>`, http.StatusOK, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates, claims, lists atomic.Int32
			provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					lists.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if claims.Load() != 1 {
					t.Error("native creation preceded durable claim")
				}
				creates.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			id, err := InitiateMultipartUpload(t.Context(), provider, "bucket", MultipartCreateRequest{SessionID: "session", Key: "key"}, ResolvedObjectEncryption{}, func(context.Context) error {
				claims.Add(1)
				return nil
			})
			if !errors.Is(err, tc.wantErr) || creates.Load() != 1 || claims.Load() != 1 || lists.Load() != 0 || err == nil && id != "original" {
				t.Fatalf("id=%q err=%v creates=%d claims=%d lists=%d", id, err, creates.Load(), claims.Load(), lists.Load())
			}
		})
	}
}

func TestS3MultipartInitiationDeniedClaimDoesNotWrite(t *testing.T) {
	var writes atomic.Int32
	provider := listingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writes.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	})
	denied := errors.New("original dispatch already claimed")
	if id, err := InitiateMultipartUpload(t.Context(), provider, "bucket", MultipartCreateRequest{SessionID: "session", Key: "key"}, ResolvedObjectEncryption{}, func(context.Context) error { return denied }); id != "" || !errors.Is(err, denied) || writes.Load() != 0 {
		t.Fatal(id, err, writes.Load())
	}
	if _, err := InitiateMultipartUpload(t.Context(), &legacyInitiationProvider{}, "bucket", MultipartCreateRequest{SessionID: "session", Key: "key"}, ResolvedObjectEncryption{}, func(context.Context) error { t.Fatal("legacy adapter claimed dispatch"); return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
