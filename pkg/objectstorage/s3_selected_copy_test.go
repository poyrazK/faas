package objectstorage

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// adr: 543
func TestS3SelectedCopySnapshot(t *testing.T) {
	for _, part := range []bool{false, true} {
		for _, tc := range []struct {
			name, selected, returned, marker string
			status                           int
			duplicate                        bool
			want                             error
		}{
			{name: "exact", selected: "old/+%?&=", returned: "old/+%?&=", status: 200},
			{name: "wrong", selected: "old", returned: "new", status: 200, want: ErrUnavailable},
			{name: "missing", selected: "old", status: 200, want: ErrUnavailable},
			{name: "duplicate", selected: "old", returned: "old", status: 200, duplicate: true, want: ErrUnavailable},
			{name: "null", selected: "null", returned: "null", status: 200},
			{name: "unversioned null", selected: "null", status: 200},
			{name: "null selected new", selected: "null", returned: "new", status: 200, want: ErrUnavailable},
			{name: "selected marker", selected: "marker", returned: "marker", marker: "true", status: 405, want: ErrInvalid},
			{name: "wrong marker", selected: "marker", returned: "other", marker: "true", status: 405, want: ErrUnavailable},
			{name: "malformed marker", selected: "old", returned: "old", marker: "maybe", status: 200, want: ErrUnavailable},
			{name: "deleted version", selected: "old", status: 404, want: ErrNotFound},
			{name: "unavailable", selected: "old", status: 503, want: ErrUnavailable},
		} {
			t.Run(strconv.FormatBool(part)+"/"+tc.name, func(t *testing.T) {
				calls := 0
				key := "folder/old 世界+%?versionId=key"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodHead || r.URL.Path != "/physical/"+key || r.URL.Query().Get("versionId") != tc.selected || r.Header.Get("Authorization") == "" {
						t.Error("HEAD did not select the signed exact source")
					}
					w.Header().Set("Content-Length", "9")
					w.Header().Set("ETag", `"old"`)
					w.Header().Set("X-Amz-Version-Id", tc.returned)
					if tc.marker != "" {
						w.Header().Set("X-Amz-Delete-Marker", tc.marker)
					}
					if tc.duplicate {
						w.Header().Add("X-Amz-Version-Id", "other")
					}
					w.WriteHeader(tc.status)
				}))
				defer server.Close()
				config := testBackend()
				config.Endpoint = server.URL
				p, err := NewS3(config, testCredentials)
				if err != nil {
					t.Fatal(err)
				}
				var s CopySourceSnapshot
				if part {
					s, err = p.(VersionedMultipartPartCopier).SnapshotVersionMultipartCopySource(t.Context(), "physical", key, tc.selected)
				} else {
					s, err = p.(VersionedTrackedObjectCopier).SnapshotVersionCopySource(t.Context(), "physical", key, tc.selected)
				}
				if !errors.Is(err, tc.want) || calls != 1 || err == nil && s.ProviderVersionID != tc.selected {
					t.Fatal(s, err, calls)
				}
			})
		}
	}
}

func TestS3SelectedCopyResponseIdentity(t *testing.T) {
	for _, part := range []bool{false, true} {
		for _, tc := range []struct {
			name, returned string
			duplicate      bool
			want           error
		}{
			{name: "matching", returned: "old"}, {name: "optional header omitted"},
			{name: "wrong", returned: "new", want: ErrUnavailable},
			{name: "duplicate", returned: "old", duplicate: true, want: ErrUnavailable},
		} {
			t.Run(strconv.FormatBool(part)+"/"+tc.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("X-Amz-Copy-Source-Version-Id", tc.returned)
					if tc.duplicate {
						w.Header().Add("X-Amz-Copy-Source-Version-Id", "other")
					}
					result := "CopyObjectResult"
					if part {
						result = "CopyPartResult"
					}
					_, _ = io.WriteString(w, `<`+result+`><ETag>&quot;copied&quot;</ETag></`+result+`>`)
				}))
				defer server.Close()
				config := testBackend()
				config.Endpoint = server.URL
				p, err := NewS3(config, testCredentials)
				if err != nil {
					t.Fatal(err)
				}
				s := CopySourceSnapshot{SizeBytes: 9, ETag: `"source"`, ProviderVersionID: "old"}
				if part {
					_, err = p.(MultipartPartCopier).CopyMultipartPart(t.Context(), "physical", MultipartPartCopyRequest{SourceKey: "source", Key: "destination", ProviderUploadID: "private", PartNumber: 1, SourceProviderVersionID: "old"}, s)
				} else {
					_, err = p.(TrackedObjectCopier).CopyTrackedObject(t.Context(), "physical", uuid.NewString(), CopyObjectRequest{SourceKey: "source", DestinationKey: "destination", SourceProviderVersionID: "old"}, s)
				}
				if !errors.Is(err, tc.want) || calls != 1 || errors.Is(err, ErrWriteRejected) {
					t.Fatal(err, calls)
				}
			})
		}
	}
}

func TestSelectedCopyPrivateRequestIdentities(t *testing.T) {
	for _, request := range []any{CopyObjectRequest{SourceProviderVersionID: "private-native"}, MultipartPartCopyRequest{SourceProviderVersionID: "private-native"}} {
		raw, err := json.Marshal(request)
		if err != nil || strings.Contains(string(raw), "private-native") || strings.Contains(string(raw), "SourceProviderVersionID") {
			t.Fatal("native request identity serialized", string(raw), err)
		}
	}
}
