package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

// adr: 818
func TestConditionalObjectSignRequest(t *testing.T) {
	for _, condition := range []map[string]string{{"if_match": `"old"`}, {"if_none_match": "*"}} {
		t.Run(condition["if_match"]+condition["if_none_match"], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for name, value := range condition {
					if body[name] != value {
						t.Error("SDK lost write condition")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"url":"https://s3.example.test/assets/key","method":"PUT","headers":{"If-None-Match":"*"},"expires_at":"2026-10-08T20:00:00Z"}`))
			}))
			defer server.Close()
			client, err := faas.NewClient(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			size := int64(3)
			_, err = client.SignBucketObject(context.Background(), "demo", "bucket", faas.ObjectSignRequest{Method: "PUT", Key: "key", SizeBytes: &size, IfMatch: condition["if_match"], IfNoneMatch: condition["if_none_match"]})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
