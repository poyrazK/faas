package faas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestWriteProtectionClientRequests(t *testing.T) {
	until := time.Date(2027, 1, 2, 3, 4, 5, 123000000, time.UTC)
	p := &faas.ObjectWriteProtection{Retention: &faas.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, LegalHold: &faas.ObjectVersionLegalHold{Status: "ON"}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Protection *faas.ObjectWriteProtection `json:"protection"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Protection == nil || body.Protection.Retention == nil || !body.Protection.Retention.RetainUntilDate.Equal(until) || body.Protection.LegalHold.Status != "ON" {
			t.Error(body)
		}
		if r.URL.Path == "/v1/apps/demo/buckets/bucket/signed-url" {
			_, _ = fmt.Fprint(w, `{"method":"PUT","url":"https://s3.example.test/assets/key","headers":{"X-Amz-Object-Lock-Legal-Hold":"ON"}}`)
		} else {
			_, _ = fmt.Fprint(w, `{"id":"upload","state":"active"}`)
		}
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	size := int64(3)
	if _, err = c.SignBucketObject(context.Background(), "demo", "bucket", faas.ObjectSignRequest{Method: "PUT", Key: "key", SizeBytes: &size, Protection: p}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateObjectMultipartUpload(context.Background(), "demo", "bucket", faas.CreateObjectMultipartUploadRequest{Key: "key", SizeBytes: 3, Protection: p}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
