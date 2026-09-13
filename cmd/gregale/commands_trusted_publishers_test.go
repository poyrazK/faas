package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrustedPublishersEmptyJSONEmitsNoProse(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/trusted_signers" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppTrustedSignerListResponse{Signers: []api.TrustedSigner{}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	if code := cmdTrustedPublishersList([]string{"demo"}); code != 0 {
		t.Fatalf("trusted-publishers list = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("empty NDJSON stream = %q, want no records", stdout.String())
	}
}
