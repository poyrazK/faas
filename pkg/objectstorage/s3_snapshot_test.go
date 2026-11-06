package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestS3VersionedEnvironmentSnapshotPinsCopySource(t *testing.T) {
	var copySource string
	var copied bool
	var receipt string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("versioning"):
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
		case r.Method == http.MethodGet && r.URL.Query().Has("versions") && r.URL.Query().Get("key-marker") == "":
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>data.json</NextKeyMarker><NextVersionIdMarker>new-version</NextVersionIdMarker><Version><Key>data.json</Key><VersionId>new-version</VersionId><IsLatest>true</IsLatest><LastModified>2026-09-29T12:00:01Z</LastModified><Size>5</Size><ETag>&quot;new&quot;</ETag></Version></ListVersionsResult>`)
		case r.Method == http.MethodGet && r.URL.Query().Has("versions") && r.URL.Query().Get("key-marker") == "data.json":
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>data.json</Key><VersionId>old+version</VersionId><IsLatest>false</IsLatest><LastModified>2026-09-29T11:59:59Z</LastModified><Size>4</Size><ETag>&quot;old&quot;</ETag></Version></ListVersionsResult>`)
		case r.Method == http.MethodHead && r.URL.Path == "/source/data.json":
			w.Header().Set("Content-Length", "4")
			w.Header().Set("ETag", `"old"`)
			w.Header().Set("X-Amz-Version-Id", "old+version")
		case r.Method == http.MethodHead && r.URL.Path == "/destination/data.json":
			if !copied {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", "4")
			w.Header().Set("ETag", `"copied"`)
			w.Header().Set("X-Amz-Version-Id", "destination-v1")
			w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
		case r.Method == http.MethodGet && r.URL.Query().Get("versionId") == "old+version":
			_, _ = io.WriteString(w, "old!")
		case r.Method == http.MethodGet && r.URL.Path == "/destination/data.json":
			_, _ = io.WriteString(w, "old!")
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			copySource = r.Header.Get("X-Amz-Copy-Source")
			if r.Header.Get("If-None-Match") != "*" || copied {
				t.Error("snapshot copy can overwrite its destination")
			}
			receipt, copied = r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey), true
			_, _ = io.WriteString(w, `<CopyObjectResult><LastModified>2026-09-29T12:00:10Z</LastModified><ETag>copied</ETag></CopyObjectResult>`)
		default:
			t.Errorf("unexpected S3 snapshot request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	config := testBackend()
	config.Endpoint = upstream.URL
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	snapshotter := provider.(VersionedObjectLister)
	cutoff := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	manifest, err := CaptureObjectManifest(context.Background(), snapshotter, "source", cutoff, 2)
	if err != nil || len(manifest) != 1 || manifest[0].VersionID != "old+version" {
		t.Fatalf("S3 manifest = %+v, %v", manifest, err)
	}
	_, err = CopyAndVerifyObjectVersion(context.Background(), provider.(ObjectSnapshotCopier), "source", "destination", manifest[0])
	if err != nil || !strings.Contains(copySource, "versionId=old%2Bversion") {
		t.Fatalf("version-pinned S3 copy = %v, source header %q", err, copySource)
	}
}
