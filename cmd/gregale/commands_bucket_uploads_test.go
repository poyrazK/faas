package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// adr: 628
func TestBucketUploadsCLIUsesOwnedSessionEndpoints(t *testing.T) {
	bucket, upload := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name              string
		args              []string
		path, query, body string
	}{
		{"list", []string{"list", "demo", bucket, "--limit=3", "--cursor=" + upload}, "/v1/apps/demo/buckets/" + bucket + "/multipart-uploads", "cursor=" + upload + "&limit=3", `{"items":[]}`},
		{"status", []string{"status", "demo", bucket, upload}, "/v1/apps/demo/buckets/" + bucket + "/multipart-uploads/" + upload, "", `{"id":"` + upload + `","state":"completed"}`},
		{"parts", []string{"parts", "demo", bucket, upload, "--limit=7", "--part-number-marker=2"}, "/v1/apps/demo/buckets/" + bucket + "/multipart-uploads/" + upload + "/parts", "limit=7&part_number_marker=2", `{"items":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != tc.path || r.URL.RawQuery != tc.query || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("incorrect session endpoint", r.URL.Path, r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fixture-token")
			var out bytes.Buffer
			previous := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = previous })
			if code := cmdBucketUploads(tc.args); code != 0 || out.Len() == 0 {
				t.Fatal(code, out.String())
			}
		})
	}
}
