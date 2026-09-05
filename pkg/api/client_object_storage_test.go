package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectStorage(t *testing.T) {
	for _, method := range []string{"list", "create", "delete-bucket", "objects", "delete-object", "sign"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer gregale-test-token" {
					t.Error("missing authentication")
				}
				if method == "objects" && (r.URL.Query().Get("prefix") != "folder +/" || r.URL.Query().Get("cursor") != "opaque+token") {
					t.Error("query encoding")
				}
				if method == "delete-object" && r.URL.Query().Get("key") != "folder/a +&世界.txt" {
					t.Error("key encoding")
				}
				w.Header().Set("Content-Type", "application/json")
				if method == "delete-bucket" || method == "delete-object" {
					if r.Method != http.MethodDelete {
						t.Error(r.Method)
					}
					w.WriteHeader(204)
					return
				}
				if method == "create" || method == "sign" {
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
			case "list":
				_, err = client.ListObjectBuckets(ctx, "demo")
			case "create":
				_, err = client.CreateObjectBucket(ctx, "demo", CreateObjectBucketRequest{Name: "assets"})
			case "delete-bucket":
				err = client.DeleteObjectBucket(ctx, "demo", "bucket")
			case "objects":
				_, err = client.ListBucketObjects(ctx, "demo", "bucket", "folder +/", "opaque+token", 100)
			case "delete-object":
				err = client.DeleteBucketObject(ctx, "demo", "bucket", "folder/a +&世界.txt")
			case "sign":
				_, err = client.SignBucketObject(ctx, "demo", "bucket", ObjectSignRequest{Method: "GET", Key: "file"})
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
