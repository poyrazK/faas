package objectstorage

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// adr: 541
func TestS3VersionedSourceCopy(t *testing.T) {
	for _, part := range []bool{false, true} {
		for _, version := range []string{"native/+%&=?", "null", ""} {
			t.Run(strconv.FormatBool(part)+"/"+version, func(t *testing.T) {
				key := "folder/hello 世界?versionId=customer.txt"
				copies := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodHead {
						w.Header().Set("Content-Length", "9")
						w.Header().Set("ETag", `"same-etag"`)
						w.Header().Set("X-Amz-Version-Id", version)
						w.Header().Set("Content-Type", "image/png")
						w.Header().Set("X-Amz-Meta-Owner", "original")
						return
					}
					copies++
					rawPath, query, _ := strings.Cut(r.Header.Get("X-Amz-Copy-Source"), "?")
					path, err := url.PathUnescape(rawPath)
					q, qe := url.ParseQuery(query)
					wantVersion := version
					if err != nil || qe != nil || path != "physical/"+key || q.Get("versionId") != wantVersion || len(q) > 1 || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"same-etag"` {
						t.Error("copy did not retain the measured source identity", path, q, err, qe)
					}
					// The latest object may now have the same ETag but a larger
					// body and different metadata. A native copy must use the old
					// version and its captured metadata, not that latest object.
					if !part && (r.Header.Get("Content-Type") != "image/png" || r.Header.Get("X-Amz-Meta-Owner") != "original") {
						t.Error("COPY did not preserve inspected metadata")
					}
					if part {
						_, _ = io.WriteString(w, `<CopyPartResult><ETag>&quot;part&quot;</ETag></CopyPartResult>`)
					} else {
						_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copied&quot;</ETag></CopyObjectResult>`)
					}
				}))
				defer upstream.Close()
				config := testBackend()
				config.Endpoint = upstream.URL
				p, err := NewS3(config, testCredentials)
				if err != nil {
					t.Fatal(err)
				}
				if part {
					s, err := p.(MultipartPartCopier).SnapshotMultipartCopySource(t.Context(), "physical", key)
					if err != nil {
						t.Fatal(err)
					}
					_, err = p.(MultipartPartCopier).CopyMultipartPart(t.Context(), "physical", MultipartPartCopyRequest{SourceKey: key, Key: "destination", ProviderUploadID: "private-upload", PartNumber: 1}, s)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					s, err := p.(TrackedObjectCopier).SnapshotCopySource(t.Context(), "physical", key)
					if err != nil || s.SizeBytes != 9 || s.ProviderVersionID != version {
						t.Fatal(s, err)
					}
					raw, err := json.Marshal(s)
					if err != nil || strings.Contains(string(raw), "ProviderVersionID") || version != "" && version != "null" && strings.Contains(string(raw), version) {
						t.Fatal("private source identity serialized", err)
					}
					_, err = p.(TrackedObjectCopier).CopyTrackedObject(t.Context(), "physical", uuid.NewString(), CopyObjectRequest{SourceKey: key, DestinationKey: "destination"}, s)
					if err != nil {
						t.Fatal(err)
					}
				}
				if copies != 1 {
					t.Fatal("copy retried", copies)
				}
			})
		}
	}
}

func TestS3CopySourceDates(t *testing.T) {
	date := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, part := range []bool{false, true} {
		for _, tc := range []struct {
			name, version, match, none string
			modified, unmodified       bool
			status                     int
			want                       error
			calls                      int
		}{
			{name: "modified", version: "native", modified: true, status: 200, calls: 1},
			{name: "unmodified", version: "native", unmodified: true, status: 200, calls: 1},
			{name: "provider date rejected", version: "native", unmodified: true, status: 412, want: ErrPreconditionFailed, calls: 1},
			{name: "matching ETag overrides unmodified", version: "native", match: `"source"`, unmodified: true, status: 200, calls: 1},
			{name: "excluded ETag", version: "native", none: `"source"`, modified: true, want: ErrPreconditionFailed},
			{name: "nonmatching ETag and modified", version: "native", none: `"other"`, modified: true, status: 200, calls: 1},
			{name: "both dates", version: "native", modified: true, unmodified: true, status: 200, calls: 1},
			{name: "deleted native source", version: "native", unmodified: true, status: 404, want: ErrNotFound, calls: 1},
			{name: "uncertain response", version: "native", unmodified: true, status: 503, want: ErrUnavailable, calls: 1},
			{name: "mutable unmodified", unmodified: true, want: ErrUnsupported},
			{name: "null unmodified", version: "null", unmodified: true, want: ErrUnsupported},
			{name: "mutable modified", modified: true, want: ErrUnsupported},
			{name: "mutable customer match", match: `"source"`, unmodified: true, status: 200, calls: 1},
		} {
			t.Run(strconv.FormatBool(part)+"/"+tc.name, func(t *testing.T) {
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					wantMatch := `"source"`
					if tc.version == "native" && tc.match == "" {
						wantMatch = ""
					}
					if r.Header.Get("X-Amz-Copy-Source-If-Match") != wantMatch || r.Header.Get("X-Amz-Copy-Source-If-None-Match") != tc.none {
						t.Error("source predicate precedence changed")
					}
					for _, d := range []struct {
						name    string
						present bool
					}{{"X-Amz-Copy-Source-If-Modified-Since", tc.modified}, {"X-Amz-Copy-Source-If-Unmodified-Since", tc.unmodified}} {
						want := ""
						if d.present {
							want = date.Format(http.TimeFormat)
						}
						if r.Header.Get(d.name) != want {
							t.Error("date predicate changed", d.name)
						}
					}
					w.WriteHeader(tc.status)
					if tc.status != 200 {
						code := "PreconditionFailed"
						if tc.status == 404 {
							code = "NoSuchVersion"
						}
						_, _ = io.WriteString(w, `<Error><Code>`+code+`</Code><Message>provider-secret</Message></Error>`)
						return
					}
					if part {
						_, _ = io.WriteString(w, `<CopyPartResult><ETag>&quot;part&quot;</ETag></CopyPartResult>`)
					} else {
						_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copied&quot;</ETag></CopyObjectResult>`)
					}
				}))
				defer upstream.Close()
				config := testBackend()
				config.Endpoint = upstream.URL
				p, err := NewS3(config, testCredentials)
				if err != nil {
					t.Fatal(err)
				}
				c := CopySourceConditions{IfMatch: tc.match, IfNoneMatch: tc.none}
				if tc.modified {
					c.IfModifiedSince = &date
				}
				if tc.unmodified {
					c.IfUnmodifiedSince = &date
				}
				s := CopySourceSnapshot{ETag: `"source"`, SizeBytes: 9, ProviderVersionID: tc.version}
				if part {
					_, err = p.(DateConditionalMultipartPartCopier).CopyDateConditionalMultipartPart(t.Context(), "physical", MultipartPartCopyRequest{SourceKey: "source", Key: "destination", ProviderUploadID: "private", PartNumber: 1, Conditions: c}, s)
				} else {
					_, err = p.(DateConditionalTrackedObjectCopier).CopyDateConditionalTrackedObject(t.Context(), "physical", uuid.NewString(), CopyObjectRequest{SourceKey: "source", DestinationKey: "destination"}, s, c)
				}
				if !errors.Is(err, tc.want) || calls != tc.calls {
					t.Fatal(err, calls)
				}
				if err != nil && strings.Contains(err.Error(), "provider-secret") {
					t.Fatal("private failure detail leaked")
				}
				if tc.want != nil && errors.Is(err, ErrWriteRejected) == (tc.status == 503) {
					t.Fatal("incorrect write certainty", err)
				}
			})
		}
	}
}

func TestS3CopySnapshotDeleteMarker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.Header().Set("ETag", `"marker"`)
		w.Header().Set("X-Amz-Delete-Marker", "true")
		w.Header().Set("X-Amz-Version-Id", "marker")
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.(TrackedObjectCopier).SnapshotCopySource(t.Context(), "bucket", "source"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
