package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestS3ListingDoesNotRetryWithoutAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		list func(context.Context, Provider) error
	}{
		{"objects", func(ctx context.Context, p Provider) error {
			_, err := p.ListObjects(ctx, "physical", "", "", 1)
			return err
		}},
		{"parts", func(ctx context.Context, p Provider) error {
			_, err := p.ListMultipartParts(ctx, "physical", MultipartListPartsRequest{Key: "key", ProviderUploadID: "native-upload", Limit: 1})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `<Error><Code>ServiceUnavailable</Code></Error>`)
			}))
			t.Cleanup(native.Close)
			p, err := NewS3(BackendConfig{Endpoint: native.URL, S3Region: "us-east-1", PathStyle: true}, func(string) string { return "fixture-secret" })
			if err != nil {
				t.Fatal(err)
			}
			for attempt := int32(1); attempt <= 2; attempt++ {
				if err = tc.list(t.Context(), p); !errors.Is(err, ErrUnavailable) || calls.Load() != attempt {
					t.Fatal("hidden SDK listing retry", err, calls.Load(), attempt)
				}
			}
		})
	}
}
