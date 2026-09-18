package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrustedPublishersMutationsHonorJSON(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "publisher.pem")
	key := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: bytes.Repeat([]byte{1}, 64)})
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/trusted_signers/ci" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	resetJSONOut(t)
	jsonOutput = true

	for _, tc := range []struct {
		name string
		run  func() int
		want string
	}{
		{name: "add", run: func() int { return cmdTrustedPublishersAdd([]string{"demo", "ci", keyPath}) }, want: `"added": true`},
		{name: "remove", run: func() int { return cmdTrustedPublishersRemove([]string{"demo", "ci"}) }, want: `"deleted": true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			defer func() { osStdout = oldOut }()
			if code := tc.run(); code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if got := stdout.String(); !strings.Contains(got, tc.want) || strings.Contains(got, "trusted signer") {
				t.Fatalf("JSON output = %q", got)
			}
		})
	}
}

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
