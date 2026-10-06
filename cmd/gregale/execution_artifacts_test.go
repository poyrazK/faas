package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func artifactReceipt() api.ExecutionResponse {
	content := []byte("patch")
	hash := sha256.Sum256(content)
	return api.ExecutionResponse{ID: "run", Runtime: api.ExecutionRuntimeNode22, Status: api.ExecutionStatusSucceeded, Artifacts: []api.ExecutionArtifact{{Name: "nested/patch.diff", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(hash[:])}}}
}

func TestExecutionArtifactSaveValidatesAndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	receipt := artifactReceipt()
	if err := saveExecutionArtifacts(receipt, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested", "patch.diff"))
	if err != nil || string(data) != "patch" {
		t.Fatalf("saved = %q, %v", data, err)
	}
	if err := saveExecutionArtifacts(receipt, dir); err == nil {
		t.Fatal("overwrote existing output")
	}
	receipt.Artifacts[0].Content[0] = 'X'
	if err := saveExecutionArtifacts(receipt, t.TempDir()); err == nil {
		t.Fatal("saved checksum mismatch")
	}
	receipt = artifactReceipt()
	receipt.Artifacts[0].Name = "../escape"
	if err := saveExecutionArtifacts(receipt, t.TempDir()); err == nil {
		t.Fatal("saved traversal")
	}
	// An existing directory symlink must not escape the selected destination.
	dir = t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := saveExecutionArtifacts(artifactReceipt(), dir); err == nil {
		t.Fatal("followed symlink outside destination")
	}
}

func TestCmdRunSelectsAndSavesArtifacts(t *testing.T) {
	receipt := artifactReceipt()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var request api.CreateExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if len(request.OutputFiles) != 1 || request.OutputFiles[0] != "nested/patch.diff" {
				t.Errorf("selection = %q", request.OutputFiles)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, _, restore := swapIO(t)
	defer restore()
	dir := t.TempDir()
	if code := cmdRun([]string{"--source", "export default () => null", "--output-file", "nested/patch.diff", "--output-dir", dir}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "nested", "patch.diff")); err != nil || string(data) != "patch" {
		t.Fatalf("artifact = %q, %v", data, err)
	}
	if code := cmdRuns([]string{"artifacts", "run", "--output-dir", t.TempDir()}); code != 0 {
		t.Fatalf("artifacts exit = %d", code)
	}
}
