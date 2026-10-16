package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIPlanIsOfflineAndDoesNotExecuteSteps(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	dir := t.TempDir()
	file := filepath.Join(dir, "gregale-ci.yaml")
	marker := filepath.Join(dir, "executed")
	data := "version: 1\nrepository: acme/api\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n    steps:\n      - name: test\n        run: touch " + marker + "\n"
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	if code := cmdCIDispatch([]string{"plan", "--file", file}); code != 0 {
		t.Fatalf("offline plan failed: %d", code)
	}
	if !strings.Contains(out.String(), `"definition_digest"`) || !strings.Contains(out.String(), `"timeout_seconds": 600`) {
		t.Fatalf("missing immutable plan: %s", out.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("planning executed the command: %v", err)
	}
}

func TestCIMetadataRequestsPreserveRunIdentity(t *testing.T) {
	revision := "a2b5b77c-c9cb-4f75-a7f4-c14f7d54be25"
	method, path, body, headers, err := ciMetadataRequest("run", "", "", "", revision, strings.Repeat("a", 40), "release-123")
	if err != nil || method != http.MethodPost || path != "/v1/internal/ci/runs" || headers.Get("Idempotency-Key") != "release-123" {
		t.Fatalf("invalid CI request: %s %s %v", method, path, err)
	}
	req, ok := body.(ci.SubmitRequest)
	if !ok || req.RevisionID != revision {
		t.Fatalf("run identity lost: %+v", body)
	}
	for _, key := range []string{"", "with spaces", strings.Repeat("x", 129)} {
		if _, _, _, _, err := ciMetadataRequest("run", "", "", "", revision, strings.Repeat("a", 40), key); err == nil {
			t.Fatal("invalid idempotency identity accepted")
		}
	}
	if _, _, _, _, err := ciMetadataRequest("cancel", "", "", "../outside", "", "", ""); err == nil {
		t.Fatal("unsafe path accepted")
	}
}

func TestCIHealthCLIUsesInternalReadOnlyAPI(t *testing.T) {
	method, path, body, headers, err := ciMetadataRequest("health", "", "", "", "", "", "")
	if err != nil || method != http.MethodGet || path != "/v1/internal/ci/health" || body != nil || len(headers) != 0 {
		t.Fatalf("health request: %s %s %v", method, path, err)
	}
}
