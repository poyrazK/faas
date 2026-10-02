package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

// adr: 405
func TestS3MutableDeleteReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, selector, version, marker, code string
		status                                int
		want                                  error
	}{
		{"unversioned", "", "", "", "", 204, nil},
		{"enabled marker", "", "private-new", "true", "", 204, nil},
		{"suspended marker", "", "null", "true", "", 204, nil},
		{"null data", "null", "null", "false", "", 204, nil},
		{"null without version header", "null", "", "", "", 204, nil},
		{"wrong selector acknowledgment", "null", "private-new", "false", "", 204, ErrUnavailable},
		{"malformed marker", "", "", "yes", "", 204, ErrUnavailable},
		{"duplicate identity", "", "duplicate", "true", "", 204, ErrUnavailable},
		{"incorrect success status", "", "", "", "", 200, ErrUnavailable},
		{"temporary failure", "", "", "", "InternalError", 500, ErrUnavailable},
		{"access denied", "", "", "", "AccessDenied", 403, ErrConfiguration},
		{"unidentified forbidden response", "", "", "", "", 403, ErrUnavailable},
		{"request timeout", "", "", "", "RequestTimeout", 408, ErrUnavailable},
		{"unsupported", "", "", "", "NotImplemented", 501, ErrUnsupported},
		{"missing bucket", "", "", "", "NoSuchBucket", 404, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "DELETE" || r.URL.Query().Get("versionId") != tc.selector || r.Header.Get("Authorization") == "" {
					t.Error("wrong mutation", r.URL)
				}
				if tc.version != "" {
					w.Header().Set("X-Amz-Version-Id", tc.version)
				}
				if tc.version == "duplicate" {
					w.Header().Add("X-Amz-Version-Id", tc.version)
				}
				if tc.marker != "" {
					w.Header().Set("X-Amz-Delete-Marker", tc.marker)
				}
				w.WriteHeader(tc.status)
				if tc.code != "" {
					_, _ = io.WriteString(w, "<Error><Code>"+tc.code+"</Code></Error>")
				}
			})).(MutableObjectDeleter)
			result, e := p.DeleteMutableObject(t.Context(), "bucket", "key /+%.txt", tc.selector)
			if !errors.Is(e, tc.want) || calls.Load() != 1 {
				t.Fatal(result, e, calls.Load())
			}
			if errors.Is(e, ErrDeletionRejected) != (tc.name == "access denied") {
				t.Fatal("unproven rejection", e)
			}
			if e == nil && result.DeleteMarker != (tc.marker == "true") {
				t.Fatal(result)
			}
		})
	}
}
