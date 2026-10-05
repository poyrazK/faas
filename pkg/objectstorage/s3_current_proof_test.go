package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestS3CurrentWriteRejectsAmbiguousProofHeaders(t *testing.T) {
	for _, phase := range []string{"acknowledgment", "recovery"} {
		for _, tc := range []struct{ name, header, value string }{
			{"duplicate etag", "ETag", `"other"`},
			{"duplicate version", "X-Amz-Version-Id", "other"},
			{"oversized version", "X-Amz-Version-Id", strings.Repeat("v", api.ObjectProviderVersionIDMaxBytes+1)},
			{"delete marker", "X-Amz-Delete-Marker", "true"},
			{"duplicate marker", "X-Amz-Delete-Marker", "false"},
			{"duplicate receipt", "X-Amz-Meta-" + ReservedUploadReceiptMetadataKey, "duplicate"},
		} {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				receipt := uuid.NewString()
				p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("ETag", `"etag"`)
					w.Header().Set("X-Amz-Version-Id", "native")
					w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
					if tc.name == "duplicate marker" {
						w.Header().Set(tc.header, "false")
					}
					if tc.name == "oversized version" || tc.name == "delete marker" {
						w.Header().Set(tc.header, tc.value)
					} else {
						w.Header().Add(tc.header, tc.value)
					}
					if r.Method == http.MethodHead {
						w.Header().Set("Content-Length", "3")
					}
				}).(TrackedObjectWriter)
				var err error
				if phase == "acknowledgment" {
					_, err = p.WriteTrackedObject(t.Context(), "bucket", "key", receipt, strings.NewReader("abc"), 3, ObjectMetadata{})
				} else {
					_, err = p.ConfirmTrackedObject(t.Context(), "bucket", "key", receipt, 3)
				}
				if err == nil || errors.Is(err, ErrWriteRejected) {
					t.Fatal("ambiguous proof settled an accepted write", err)
				}
			})
		}
	}
}
