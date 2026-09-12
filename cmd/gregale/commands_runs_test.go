package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdRunSubmitJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/executions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["runtime"] != "node24" || body["source"] != "console.log(1)" {
			t.Fatalf("body = %#v", body)
		}
		if network, ok := body["network"].(map[string]any); !ok || network["mode"] != "none" {
			t.Fatalf("network = %#v", body["network"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"01234567-89ab-4cde-8012-3456789abcde","status":"queued","runtime":"node24","limits":{"timeout_ms":5000,"memory_mb":128,"cpu_millicores":250,"ephemeral_disk_mb":64,"max_output_bytes":262144,"pids_max":64},"output_truncated":false,"created_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	out, _, restore := swapIO(t)
	defer restore()

	if got := cmdRun([]string{"--runtime", "node24", "--source", "console.log(1)", "--input", `{"x":1}`}); got != 0 {
		t.Fatalf("cmdRun exit = %d", got)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatalf("receipt JSON: %v; output=%q", err, out.String())
	}
	if receipt["id"] != "01234567-89ab-4cde-8012-3456789abcde" || receipt["status"] != "queued" {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestExecutionSourceRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "source.js")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.js")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := executionSource("", link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("executionSource symlink error = %v", err)
	}
	if _, err := resolveExecutionInput("@" + link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("resolveExecutionInput symlink error = %v", err)
	}
}

func TestCmdRunSubmitsDirectoryBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.mjs"), []byte("export default () => 42"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.mjs"), []byte("export const answer = 41"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Runtime    string `json:"runtime"`
			Source     string `json:"source"`
			Entrypoint string `json:"entrypoint"`
			Files      []struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			} `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Runtime != "node24" || body.Source != "" || body.Entrypoint != "src/main.mjs" || len(body.Files) != 2 {
			t.Fatalf("bundle request = %#v", body)
		}
		for _, file := range body.Files {
			if _, err := base64.StdEncoding.DecodeString(file.Content); err != nil {
				t.Fatalf("file %q content was not base64: %v", file.Path, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"01234567-89ab-4cde-8012-3456789abcde","status":"queued","runtime":"node24","limits":{"timeout_ms":5000,"memory_mb":128,"cpu_millicores":250,"ephemeral_disk_mb":64,"max_output_bytes":262144,"pids_max":64},"output_truncated":false,"created_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	out, _, restore := swapIO(t)
	defer restore()
	if got := cmdRun([]string{"--runtime", "node24", "--dir", dir, "--entrypoint", "src/main.mjs"}); got != 0 {
		t.Fatalf("cmdRun exit = %d", got)
	}
	if !strings.Contains(out.String(), `"status": "queued"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestExecutionBundleRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := executionBundle(dir, "secret.txt"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("executionBundle error = %v", err)
	}
}
