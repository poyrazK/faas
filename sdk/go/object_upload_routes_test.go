package faas_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestEncryptedUploadRouteClient(t *testing.T) {
	const key = "arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("management authorization missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var in faas.CreateObjectUploadRouteRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Encryption == nil || in.Encryption.KeyID != key || r.URL.EscapedPath() != "/v1/apps/demo%20app/upload-routes" {
				t.Error("owned route encryption missing")
			}
			_, _ = io.WriteString(w, `{"id":"route","name":"files","bucket_id":"bucket","encryption":{"algorithm":"aws:kms","key_id":"`+key+`"}}`)
		case http.MethodGet:
			if r.URL.EscapedPath() == "/v1/apps/demo%20app/buckets/bucket/write-receipts/receipt" {
				_, _ = io.WriteString(w, `{"id":"receipt","status":"completed","encryption":{"algorithm":"aws:kms","key_id":"`+key+`"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"items":[{"id":"route","name":"files","bucket_id":"bucket","encryption":{"algorithm":"aws:kms","key_id":"`+key+`"}}]}`)
		case http.MethodDelete:
			if r.URL.EscapedPath() != "/v1/apps/demo%20app/upload-routes/files" {
				t.Error("wrong deletion route")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected method")
		}
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	route, err := client.CreateObjectUploadRoute(context.Background(), "demo app", faas.CreateObjectUploadRouteRequest{Name: "files", BucketID: "bucket", Encryption: &faas.ObjectEncryption{Algorithm: "aws:kms", KeyID: key}})
	if err != nil || route.Encryption == nil || route.Encryption.KeyID != key {
		t.Fatal(route, err)
	}
	list, err := client.ListObjectUploadRoutes(context.Background(), "demo app")
	if err != nil || len(list.Items) != 1 || list.Items[0].Encryption.KeyID != key {
		t.Fatal(list, err)
	}
	if err = client.DeleteObjectUploadRoute(context.Background(), "demo app", "files"); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.GetObjectWriteReceipt(context.Background(), "demo app", "bucket", "receipt")
	if err != nil || receipt.Encryption == nil || receipt.Encryption.KeyID != key {
		t.Fatal("receipt lost owned encryption", receipt, err)
	}
	if calls != 4 {
		t.Fatal("unexpected management requests", calls)
	}
}
