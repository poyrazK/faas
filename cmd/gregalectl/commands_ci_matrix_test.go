package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIMatrixCLIPlanExpandsOffline(t *testing.T) {
	previousJSON, previousOut := jsonOutput, osStdout
	jsonOutput = true
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { jsonOutput, osStdout = previousJSON, previousOut })
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	file := filepath.Join(dir, "matrix.yaml")
	raw := "version: 1\nrepository: gregale/example\njobs:\n  test:\n    image: registry.example/ci@sha256:" + strings.Repeat("a", 64) + "\n    matrix:\n      alpha: {env: {MODE: alpha}}\n      beta: {env: {MODE: beta}}\n    steps:\n      - name: unit\n        run: touch " + marker + "\n"
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdCIDispatch([]string{"plan", "--file", file}); code != 0 {
		t.Fatalf("matrix planning failed: %d", code)
	}
	var plan ci.Plan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Jobs) != 2 || plan.Jobs[0].Name != "test-alpha" || plan.Jobs[1].Name != "test-beta" || plan.Jobs[0].Env["MODE"] != "alpha" || plan.Jobs[1].Env["MODE"] != "beta" || plan.Jobs[0].Matrix != nil {
		t.Fatal("CLI lost concrete matrix names/configuration")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("matrix planning executed source commands")
	}
}
