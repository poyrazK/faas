package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPNativeReleaseCanonicalSourcesFenceJournal(t *testing.T) {
	root := t.TempDir()
	realRoot, alias := filepath.Join(root, "sources"), filepath.Join(root, "alias")
	for _, name := range []string{"web", "worker"} {
		dir := filepath.Join(realRoot, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	plan := main.McpNativeReleasePlan{WebApp: "web", WorkerApp: "candidate", ObserverApp: "observer", ObserverMetricApp: "candidate", WebPath: filepath.Join(alias, "web"), WorkerPath: filepath.Join(alias, "worker")}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "plan.json")
	if err := os.WriteFile(planPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := main.ReadMCPNativePlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebPath != filepath.Join(realRoot, "web") || loaded.WorkerPath != filepath.Join(realRoot, "worker") {
		t.Fatalf("noncanonical paths: %s %s", loaded.WebPath, loaded.WorkerPath)
	}
	for _, source := range []string{"web", "worker"} {
		for _, parent := range []string{realRoot, alias} {
			t.Run(source+"/"+filepath.Base(parent), func(t *testing.T) {
				statePath := filepath.Join(parent, source, "release.json")
				if code := main.Run([]string{"mcp", "tasks", "release", "status", "--plan", planPath, "--state", statePath, "--json"}); code == 0 {
					t.Fatal("accepted journal inside deployment source")
				}
				if _, err := os.Stat(statePath); !os.IsNotExist(err) {
					t.Fatalf("journal created: %v", err)
				}
			})
		}
	}
	// An external journal must leave both the source fingerprint and resume valid.
	fingerprint, err := main.McpNativeFingerprint(loaded)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "release.json")
	state, err := main.LoadMCPNativeState(statePath, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := main.SaveMCPNativeState(statePath, &state); err != nil {
		t.Fatal(err)
	}
	if code := main.Run([]string{"mcp", "tasks", "release", "status", "--plan", planPath, "--state", statePath, "--json"}); code != 0 {
		t.Fatalf("external journal cannot resume: %d", code)
	}
}
