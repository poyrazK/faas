// adr: 583
package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestS3SnapshotRetentionObservesActualPinnedVersion(t *testing.T) {
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	until := created.Add(3 * time.Hour)
	item := ObjectVersion{Key: "folder/data.json", VersionID: "old+version", Size: 3, ETag: `"etag"`, LastModified: created}
	for _, fault := range []string{"none", "governance", "legal_hold_only", "missing_lock", "missing_expiration", "short_retention", "missing_version", "wrong_version", "wrong_size", "wrong_etag", "wrong_time", "deleted"} {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodHead || r.URL.Path != "/source/folder/data.json" || r.URL.Query().Get("versionId") != item.VersionID {
					t.Errorf("unpinned retention request: %s %s", r.Method, r.URL.RequestURI())
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if fault == "deleted" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				h := w.Header()
				h.Set("x-amz-version-id", item.VersionID)
				h.Set("Content-Length", strconv.FormatInt(item.Size, 10))
				h.Set("ETag", item.ETag)
				h.Set("Last-Modified", created.Format(http.TimeFormat))
				h.Set("x-amz-object-lock-mode", "COMPLIANCE")
				h.Set("x-amz-object-lock-retain-until-date", until.Format(time.RFC3339))
				switch fault {
				case "governance":
					h.Set("x-amz-object-lock-mode", "GOVERNANCE")
				case "legal_hold_only":
					h.Del("x-amz-object-lock-mode")
					h.Set("x-amz-object-lock-legal-hold", "ON")
				case "missing_lock":
					h.Del("x-amz-object-lock-mode")
				case "missing_expiration":
					h.Del("x-amz-object-lock-retain-until-date")
				case "short_retention":
					h.Set("x-amz-object-lock-retain-until-date", created.Add(time.Hour).Format(time.RFC3339))
				case "missing_version":
					h.Del("x-amz-version-id")
				case "wrong_version":
					h.Set("x-amz-version-id", "current")
				case "wrong_size":
					h.Set("Content-Length", "9")
				case "wrong_etag":
					h.Set("ETag", `"new"`)
				case "wrong_time":
					h.Set("Last-Modified", created.Add(time.Second).Format(http.TimeFormat))
				}
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			err = VerifyObjectManifestRetention(context.Background(), provider.(ObjectVersionRetentionObserver), "source", []ObjectVersion{item}, time.Now().Add(time.Hour))
			want := ErrObjectSnapshotRetentionUnavailable
			switch fault {
			case "none":
				if err != nil || calls != 1 {
					t.Fatalf("actual retention: %v, calls %d", err, calls)
				}
				return
			case "missing_version", "wrong_version", "wrong_size", "wrong_etag", "wrong_time":
				want = ErrObjectSnapshotCopyMismatch
			case "deleted":
				want = ErrNotFound
			}
			if !errors.Is(err, want) || calls != 1 {
				t.Fatalf("retention %s: %v, want %v, calls %d", fault, err, want, calls)
			}
		})
	}
}
