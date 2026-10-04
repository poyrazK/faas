package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

// adr: 546
func TestS3VersionDeleteSingleAttemptAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, responseVersion, marker, code string
		status                              int
		want                                error
	}{
		{"data", "native/+%?", "false", "", 204, nil},
		{"marker", "native/+%?", "true", "", 204, nil},
		{"already gone", "", "", "NoSuchVersion", 404, nil},
		{"missing bucket", "", "", "NoSuchBucket", 404, ErrNotFound},
		{"wrong acknowledgment", "other-private-version", "true", "", 204, ErrUnavailable},
		{"malformed marker", "native/+%?", "maybe", "", 204, ErrUnavailable},
		{"wrong success status", "native/+%?", "false", "", 200, ErrUnavailable},
		{"duplicate version", "duplicate", "false", "", 204, ErrUnavailable},
		{"provider unavailable", "", "", "InternalError", 500, ErrUnavailable},
		{"unsupported", "", "", "NotImplemented", 501, ErrUnsupported},
		{"provider permission", "", "", "AccessDenied", 403, ErrConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "DELETE" || r.URL.Query().Get("versionId") != "native/+%?" || r.Header.Get("Authorization") == "" {
					t.Error("exact signed selector lost", r.URL)
				}
				if tc.responseVersion != "" {
					w.Header().Set("X-Amz-Version-Id", tc.responseVersion)
					if tc.responseVersion == "duplicate" {
						w.Header().Set("X-Amz-Version-Id", "native/+%?")
						w.Header().Add("X-Amz-Version-Id", "native/+%?")
					}
				}
				if tc.marker != "" {
					w.Header().Set("X-Amz-Delete-Marker", tc.marker)
				}
				w.WriteHeader(tc.status)
				if tc.code != "" {
					_, _ = io.WriteString(w, "<Error><Code>"+tc.code+"</Code><Message>private provider details</Message></Error>")
				}
			})).(ObjectVersionDeleter)
			result, err := p.DeleteObjectVersion(t.Context(), "bucket", "key /+%.txt", "native/+%?")
			if !errors.Is(err, tc.want) || calls.Load() != 1 || err == nil && result.DeleteMarker != (tc.marker == "true") {
				t.Fatal(result, err, calls.Load())
			}
			if _, err = p.DeleteObjectVersion(t.Context(), "bucket", "key", "null"); !errors.Is(err, ErrInvalid) || calls.Load() != 1 {
				t.Fatal("mutable null dispatched", err, calls.Load())
			}
		})
	}
}
