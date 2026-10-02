// adr: 375
package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

func TestGCSRetentionUsesPinnedGenerationAndLockedPolicy(t *testing.T) {
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	until := created.Add(3 * time.Hour)
	item := ObjectVersion{Key: "data.json", VersionID: "42", MetadataVersion: "3", Size: 3, ETag: "old-etag", LastModified: created}
	for _, fault := range []string{"object_locked", "bucket_locked", "unlocked_object", "unlocked_bucket", "temporary_hold", "short_retention", "missing_expiration", "subday_policy", "wrong_generation", "wrong_metadata", "wrong_bucket", "wrong_key", "wrong_size", "wrong_etag", "wrong_time", "precondition_failed"} {
		t.Run(fault, func(t *testing.T) {
			objectCalls, bucketCalls := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/b/source/o/data.json":
					objectCalls++
					if r.Method != http.MethodGet || r.URL.Query().Get("generation") != "42" || r.URL.Query().Get("ifMetagenerationMatch") != "3" {
						t.Errorf("unpinned GCS retention request: %s %s", r.Method, r.URL.RequestURI())
					}
					if fault == "precondition_failed" {
						w.WriteHeader(http.StatusPreconditionFailed)
						_, _ = w.Write([]byte(`{"error":{"code":412,"message":"metadata changed"}}`))
						return
					}
					body := map[string]any{"bucket": "source", "name": item.Key, "generation": "42", "metageneration": "3", "size": "3", "etag": item.ETag, "timeCreated": created.Format(time.RFC3339), "retentionExpirationTime": until.Format(time.RFC3339)}
					switch fault {
					case "object_locked", "short_retention":
						expires := until
						if fault == "short_retention" {
							expires = created.Add(time.Hour)
						}
						body["retention"] = map[string]string{"mode": "Locked", "retainUntilTime": expires.Format(time.RFC3339)}
					case "unlocked_object":
						body["retention"] = map[string]string{"mode": "Unlocked", "retainUntilTime": until.Format(time.RFC3339)}
					case "temporary_hold":
						body["temporaryHold"] = true
					case "missing_expiration":
						delete(body, "retentionExpirationTime")
					case "wrong_generation":
						body["generation"] = "43"
					case "wrong_metadata":
						body["metageneration"] = "4"
					case "wrong_bucket":
						body["bucket"] = "other"
					case "wrong_key":
						body["name"] = "other.json"
					case "wrong_size":
						body["size"] = "4"
					case "wrong_etag":
						body["etag"] = "new-etag"
					case "wrong_time":
						body["timeCreated"] = created.Add(time.Second).Format(time.RFC3339)
					}
					_ = json.NewEncoder(w).Encode(body)
				case "/b/source":
					bucketCalls++
					locked := fault != "unlocked_bucket" && fault != "unlocked_object" && fault != "temporary_hold"
					period := "86400"
					if fault == "subday_policy" {
						period = "60"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"name": "source", "retentionPolicy": map[string]any{"isLocked": locked, "retentionPeriod": period, "effectiveTime": created.Format(time.RFC3339)}})
				default:
					t.Errorf("unexpected retention request: %s", r.URL.RequestURI())
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer upstream.Close()
			client, err := storage.NewClient(context.Background(), option.WithEndpoint(upstream.URL), option.WithoutAuthentication(), storage.WithDisabledClientMetrics())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			provider := &GCS{store: &googleGCSStore{client: client}}
			err = VerifyObjectManifestRetention(context.Background(), provider, "source", []ObjectVersion{item}, time.Now().Add(time.Hour))
			want := ErrObjectSnapshotRetentionUnavailable
			switch fault {
			case "object_locked", "bucket_locked":
				wantBucketCalls := 1
				if fault == "object_locked" {
					wantBucketCalls = 0
				}
				if err != nil || objectCalls != 1 || bucketCalls != wantBucketCalls {
					t.Fatalf("observed retention: %v, object calls %d, bucket calls %d", err, objectCalls, bucketCalls)
				}
				return
			case "wrong_generation", "wrong_metadata", "wrong_bucket", "wrong_key", "wrong_size", "wrong_etag", "wrong_time":
				want = ErrObjectSnapshotCopyMismatch
			case "precondition_failed":
				want = ErrConflict
			}
			if !errors.Is(err, want) || objectCalls != 1 {
				t.Fatalf("retention %s: %v, want %v, object calls %d", fault, err, want, objectCalls)
			}
		})
	}
}
