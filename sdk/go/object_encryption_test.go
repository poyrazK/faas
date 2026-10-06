package faas_test

import (
	"context"
	"fmt"
	faas "github.com/poyrazK/faas/sdk/go"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnedEncryptionDiscoveryClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/v1/apps/demo%2Fx/buckets/bucket%2Fx/encryption-capabilities" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{"algorithms":["AES256","aws:kms"],"key_ids":["owned-key"]}`)
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.GetObjectBucketEncryptionCapabilities(context.Background(), "demo/x", "bucket/x")
	if err != nil || len(out.KeyIDs) != 1 || out.KeyIDs[0] != "owned-key" || len(out.Algorithms) != 2 {
		t.Fatal(out, err)
	}
}
