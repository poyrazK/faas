package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-20261026145832: deploy --plan shows a multi-workload source's split
// locally, as scan does after upload.
func TestDeployPlanShowsTheWorkloadSplit(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"compose.yaml":   "services:\n  api:\n    build: ./api\n    ports: ['3000:3000']\n  web:\n    build: ./web\n    ports: ['8080:8080']\n  db:\n    image: postgres:16\n",
		"api/Dockerfile": "FROM node:22\nCMD [\"node\", \"server.js\"]\n",
		"web/Dockerfile": "FROM nginx:1.27\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAAS_TOKEN", "")

	code, stdout, stderr := runCLI(t, "deploy", "--plan", "--path", root)
	for _, want := range []string{"Workloads (2)", "api", "web", "db", "postgres", "gregale deploy --project"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("deploy --plan = %d, missing %q:\n%s%s", code, want, stdout, stderr)
		}
	}

	code, stdout, _ = runCLI(t, "--json", "deploy", "--plan", "--path", root)
	var plan struct {
		Workloads []struct {
			Name string `json:"name"`
		} `json:"workloads"`
		Managed []struct {
			Kind string `json:"kind"`
		} `json:"managed"`
	}
	if err := json.Unmarshal([]byte(stdout), &plan); code != 0 || err != nil || len(plan.Workloads) != 2 || len(plan.Managed) != 1 || plan.Managed[0].Kind != "postgres" {
		t.Fatalf("--json deploy --plan = %d %v %+v\n%s", code, err, plan, stdout)
	}
}
