package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestS3MultipartRecoveryAfterLostReplyAndDelayedVisibility(t *testing.T) {
	var creates atomic.Int32
	var visible atomic.Bool
	provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.Method == http.MethodPost {
			creates.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `<Error><Code>SlowDown</Code></Error>`)
			return
		}
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated>`)
		if visible.Load() {
			_, _ = io.WriteString(w, `<Upload><Key>key</Key><UploadId>original</UploadId></Upload>`)
		}
		_, _ = io.WriteString(w, `</ListMultipartUploadsResult>`)
	})
	request := MultipartCreateRequest{SessionID: "session", Key: "key"}
	if _, err := provider.EnsureMultipartUpload(t.Context(), "bucket", request); !errors.Is(err, ErrUnavailable) || creates.Load() != 1 {
		t.Fatalf("lost reply: %v, creates=%d", err, creates.Load())
	}
	config := testBackend()
	config.Endpoint = *provider.(*S3).client.Options().BaseEndpoint
	restarted, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := RecoverMultipartUpload(t.Context(), restarted, "bucket", request); id != "" || !errors.Is(err, ErrNotFound) || creates.Load() != 1 {
		t.Fatalf("negative observation retried creation: %q, %v, creates=%d", id, err, creates.Load())
	}
	visible.Store(true)
	if id, err := RecoverMultipartUpload(t.Context(), restarted, "bucket", request); id != "original" || err != nil || creates.Load() != 1 {
		t.Fatalf("delayed discovery: %q, %v, creates=%d", id, err, creates.Load())
	}
}

func TestS3MultipartRecoveryNeverCreates(t *testing.T) {
	for _, tc := range []struct {
		name, listing, id string
		wantErr           error
	}{
		{"empty", `<IsTruncated>false</IsTruncated>`, "", ErrNotFound},
		{"candidate", `<IsTruncated>false</IsTruncated><Upload><Key>key</Key><UploadId>original</UploadId></Upload>`, "original", nil},
		{"sibling only", `<IsTruncated>false</IsTruncated><Upload><Key>key-sibling</Key><UploadId>sibling</UploadId></Upload>`, "", ErrNotFound},
		{"ambiguous", `<IsTruncated>false</IsTruncated><Upload><Key>key</Key><UploadId>one</UploadId></Upload><Upload><Key>key</Key><UploadId>two</UploadId></Upload>`, "", ErrConflict},
		{"incomplete", `<Upload><Key>key</Key><UploadId>original</UploadId></Upload>`, "", ErrUnavailable},
		{"invalid identity", `<IsTruncated>false</IsTruncated><Upload><Key>key</Key><UploadId> </UploadId></Upload>`, "", ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lists, writes atomic.Int32
			provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Query().Get("uploads") != "" || !r.URL.Query().Has("uploads") {
					writes.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				lists.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult>`+tc.listing+`</ListMultipartUploadsResult>`)
			})
			request := MultipartCreateRequest{SessionID: "session", Key: "key", SizeBytes: 10}
			// Repeat after reconstructing the adapter: no negative or uncertain
			// observation may turn recovery into a fresh creation.
			for range 2 {
				id, err := RecoverMultipartUpload(t.Context(), provider, "gregale-test", request)
				if id != tc.id || !errors.Is(err, tc.wantErr) {
					t.Fatalf("recovery = %q, %v; want %q, %v", id, err, tc.id, tc.wantErr)
				}
				config := testBackend()
				config.Endpoint = *provider.(*S3).client.Options().BaseEndpoint
				provider, err = NewS3(config, testCredentials)
				if err != nil {
					t.Fatal(err)
				}
			}
			if lists.Load() != 2 || writes.Load() != 0 {
				t.Fatalf("lists=%d writes=%d", lists.Load(), writes.Load())
			}
		})
	}
}

func TestS3MultipartRecoveryChecksAuthorityBeforeEveryPage(t *testing.T) {
	var lists atomic.Int32
	provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("recovery dispatched a write")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		lists.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextUploadIdMarker>original</NextUploadIdMarker><Upload><Key>key</Key><UploadId>original</UploadId></Upload></ListMultipartUploadsResult>`)
	})
	checks := 0
	denied := errors.New("original journal authority expired")
	request := MultipartCreateRequest{SessionID: "session", Key: "key", BeforeRequest: func(context.Context) error {
		checks++
		if checks == 2 {
			return denied
		}
		return nil
	}}
	id, err := RecoverMultipartUpload(t.Context(), provider, "gregale-test", request)
	if id != "" || !errors.Is(err, denied) || checks != 2 || lists.Load() != 1 {
		t.Fatalf("id=%q err=%v checks=%d lists=%d", id, err, checks, lists.Load())
	}
}

type legacyInitiationProvider struct {
	Provider
	creates int
}

func (p *legacyInitiationProvider) EnsureMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error) {
	p.creates++
	return "new-upload", nil
}

type invalidInitiationRecoveryProvider struct{ legacyInitiationProvider }

func (*invalidInitiationRecoveryProvider) RecoverMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error) {
	return "", nil
}

func TestMultipartRecoveryRejectsLegacyAndInvalidIdentity(t *testing.T) {
	request := MultipartCreateRequest{SessionID: "session", Key: "key"}
	legacy := &legacyInitiationProvider{}
	if id, err := RecoverMultipartUpload(t.Context(), legacy, "bucket", request); id != "" || !errors.Is(err, ErrUnsupported) || legacy.creates != 0 {
		t.Fatalf("legacy recovery fell back to create: %q, %v, creates=%d", id, err, legacy.creates)
	}
	invalid := &invalidInitiationRecoveryProvider{}
	if id, err := RecoverMultipartUpload(t.Context(), invalid, "bucket", request); id != "" || !errors.Is(err, ErrUnavailable) || invalid.creates != 0 {
		t.Fatalf("accepted invalid identity: %q, %v, creates=%d", id, err, invalid.creates)
	}
}

func TestS3MultipartRecoveryRejectsInvalidRequestBeforeIO(t *testing.T) {
	var calls atomic.Int32
	provider := listingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	})
	for _, request := range []MultipartCreateRequest{
		{Key: "key"},
		{SessionID: "session"},
		{SessionID: "session", Key: "key", SizeBytes: -1},
	} {
		for _, recoverUpload := range []func(context.Context, string, MultipartCreateRequest) (string, error){
			provider.(*S3).RecoverMultipartUpload,
			func(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
				return RecoverMultipartUpload(ctx, provider, bucket, r)
			},
		} {
			if id, err := recoverUpload(t.Context(), "bucket", request); id != "" || !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid request = %q, %v", id, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid recovery dispatched %d requests", calls.Load())
	}
}
