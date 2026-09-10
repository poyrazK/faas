package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectStorage(t *testing.T) {
	for _, method := range []string{"list", "create", "delete-bucket", "list-s3-credentials", "create-s3-credential", "revoke-s3-credential", "list-compute-bindings", "create-compute-binding", "delete-compute-binding", "rotate-compute-binding", "objects", "delete-object", "sign", "create-multipart", "list-multipart", "get-multipart", "list-parts", "sign-part", "complete-multipart", "abort-multipart", "usage", "report"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer gregale-test-token" {
					t.Error("missing authentication")
				}
				if method == "objects" && (r.URL.Query().Get("prefix") != "folder +/" || r.URL.Query().Get("cursor") != "opaque+token") {
					t.Error("query encoding")
				}
				if method == "list-multipart" && (r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("cursor") != "cursor-id") {
					t.Error("multipart upload query encoding")
				}
				if method == "list-parts" && (r.URL.Query().Get("part_number_marker") != "7" || r.URL.Query().Get("limit") != "25") {
					t.Error("multipart part query encoding")
				}
				if method == "delete-object" && r.URL.Query().Get("key") != "folder/a +&世界.txt" {
					t.Error("key encoding")
				}
				w.Header().Set("Content-Type", "application/json")
				if method == "usage" && r.URL.Path != "/v1/account/object-storage-usage" {
					t.Error(r.URL.Path)
				}
				if method == "report" {
					if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/object-storage/usage-reports" || r.Header.Get("Idempotency-Key") == "" {
						t.Error("report request contract")
					}
					w.WriteHeader(204)
					return
				}
				if method == "sign-part" && r.URL.Path != "/v1/apps/demo/buckets/bucket/multipart-uploads/upload/parts/7/signed-url" {
					t.Error(r.URL.Path)
				}
				if method == "list-parts" && r.URL.Path != "/v1/apps/demo/buckets/bucket/multipart-uploads/upload/parts" {
					t.Error(r.URL.Path)
				}
				if method == "list-multipart" && r.URL.Path != "/v1/apps/demo/buckets/bucket/multipart-uploads" {
					t.Error(r.URL.Path)
				}
				if method == "list-s3-credentials" || method == "create-s3-credential" {
					if r.URL.Path != "/v1/apps/demo/buckets/bucket/s3-credentials" {
						t.Error(r.URL.Path)
					}
				}
				if method == "revoke-s3-credential" && r.URL.Path != "/v1/apps/demo/buckets/bucket/s3-credentials/credential" {
					t.Error(r.URL.Path)
				}
				if method == "list-compute-bindings" || method == "create-compute-binding" {
					if r.URL.Path != "/v1/apps/demo/buckets/bucket/compute-bindings" {
						t.Error(r.URL.Path)
					}
				}
				if method == "delete-compute-binding" && r.URL.Path != "/v1/apps/demo/buckets/bucket/compute-bindings/binding" {
					t.Error(r.URL.Path)
				}
				if method == "rotate-compute-binding" && r.URL.Path != "/v1/apps/demo/buckets/bucket/compute-bindings/binding/rotate" {
					t.Error(r.URL.Path)
				}
				if method == "delete-bucket" || method == "delete-object" || method == "abort-multipart" || method == "revoke-s3-credential" || method == "delete-compute-binding" {
					if r.Method != http.MethodDelete {
						t.Error(r.Method)
					}
					w.WriteHeader(204)
					return
				}
				if method == "create" || method == "create-s3-credential" || method == "create-compute-binding" || method == "rotate-compute-binding" || method == "sign" || method == "create-multipart" || method == "sign-part" || method == "complete-multipart" {
					if r.Method != http.MethodPost {
						t.Error(r.Method)
					}
				} else if r.Method != http.MethodGet {
					t.Error(r.Method)
				}
				_, _ = io.WriteString(w, "{}")
			}))
			defer srv.Close()
			client := NewClient(srv.URL, "gregale-test-token")
			ctx := context.Background()
			var err error
			switch method {
			case "usage":
				_, err = client.GetObjectStorageUsage(ctx)
			case "report":
				err = client.RecordObjectStorageUsage(ctx, ObjectStorageUsageReport{})
			case "list":
				_, err = client.ListObjectBuckets(ctx, "demo")
			case "create":
				_, err = client.CreateObjectBucket(ctx, "demo", CreateObjectBucketRequest{Name: "assets"})
			case "delete-bucket":
				err = client.DeleteObjectBucket(ctx, "demo", "bucket")
			case "list-s3-credentials":
				_, err = client.ListObjectS3Credentials(ctx, "demo", "bucket")
			case "create-s3-credential":
				_, err = client.CreateObjectS3Credential(ctx, "demo", "bucket", CreateObjectS3CredentialRequest{Label: "production", Permission: "read_write"})
			case "revoke-s3-credential":
				err = client.RevokeObjectS3Credential(ctx, "demo", "bucket", "credential")
			case "list-compute-bindings":
				_, err = client.ListObjectStorageComputeBindings(ctx, "demo", "bucket")
			case "create-compute-binding":
				_, err = client.CreateObjectStorageComputeBinding(ctx, "demo", "bucket", CreateObjectStorageComputeBindingRequest{Permission: ObjectBucketPermissionReadWrite})
			case "delete-compute-binding":
				err = client.DeleteObjectStorageComputeBinding(ctx, "demo", "bucket", "binding")
			case "rotate-compute-binding":
				_, err = client.RotateObjectStorageComputeBinding(ctx, "demo", "bucket", "binding")
			case "objects":
				_, err = client.ListBucketObjects(ctx, "demo", "bucket", "folder +/", "opaque+token", 100)
			case "delete-object":
				err = client.DeleteBucketObject(ctx, "demo", "bucket", "folder/a +&世界.txt")
			case "sign":
				_, err = client.SignBucketObject(ctx, "demo", "bucket", ObjectSignRequest{Method: "GET", Key: "file"})
			case "create-multipart":
				_, err = client.CreateObjectMultipartUpload(ctx, "demo", "bucket", CreateObjectMultipartUploadRequest{Key: "large.bin", SizeBytes: 70 << 20})
			case "list-multipart":
				_, err = client.ListObjectMultipartUploads(ctx, "demo", "bucket", 25, "cursor-id")
			case "get-multipart":
				_, err = client.GetObjectMultipartUpload(ctx, "demo", "bucket", "upload")
			case "list-parts":
				_, err = client.ListObjectMultipartParts(ctx, "demo", "bucket", "upload", 7, 25)
			case "sign-part":
				_, err = client.SignObjectMultipartPart(ctx, "demo", "bucket", "upload", 7, ObjectMultipartPartSignRequest{ExpiresIn: 60})
			case "complete-multipart":
				_, err = client.CompleteObjectMultipartUpload(ctx, "demo", "bucket", "upload", CompleteObjectMultipartUploadRequest{Parts: []ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}})
			case "abort-multipart":
				err = client.AbortObjectMultipartUpload(ctx, "demo", "bucket", "upload")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
