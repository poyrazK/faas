package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdTrustedPublishersList_JSON_EmptyIsZeroRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/jnjk/trusted_signers" {
			t.Errorf("path = %q, want trusted signer list route", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.AppTrustedSignerListResponse{Signers: []api.TrustedSigner{}})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdTrustedPublishers([]string{"list", "jnjk"}); code != 0 {
		t.Fatalf("cmdTrustedPublishers list = %d, want 0", code)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("empty NDJSON list emitted %q; want zero records", got)
	}
}
