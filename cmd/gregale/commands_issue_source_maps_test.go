package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueSourceMapCLIArtifactManagement(t *testing.T) {
	dep := "11111111-1111-4111-8111-111111111111"
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/exports/deployments/"+dep+"/issue-source-maps" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		methods = append(methods, r.Method)
		if r.Method == "PUT" {
			var in api.PublishIssueSourceMapsRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Directory != "/app/dist" || len(in.Manifest.Artifacts) != 1 {
				t.Errorf("invalid upload %+v %v", in, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.IssueSourceMapsMetadata{DeploymentID: dep, MapCount: 1, Ready: r.Method != "DELETE", ManifestSHA256: strings.Repeat("a", 64)})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	file := filepath.Join(t.TempDir(), "manifest.json")
	manifest := api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("b", 64), Payload: `{"version":3,"sources":["source.ts"],"names":["charge"],"mappings":"AAAAA"}`}}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"source-maps", "publish", "--app", "exports", "--deployment", dep, "--directory", "/app/dist", "--manifest-file", file}, {"source-maps", "status", "--app", "exports", "--deployment", dep}, {"source-maps", "retire", "--app", "exports", "--deployment", dep}} {
		if code := cmdIssues(args); code != 0 {
			t.Fatalf("command failed: %v", args)
		}
	}
	if strings.Join(methods, ",") != "PUT,GET,DELETE" || !strings.Contains(out.String(), "Source maps retired") {
		t.Fatalf("incomplete journey: %v %s", methods, out.String())
	}
	if code := cmdIssues([]string{"source-maps", "publish", "--app", "exports", "--deployment", dep}); code == 0 {
		t.Fatal("upload omitted required inputs")
	}
}
