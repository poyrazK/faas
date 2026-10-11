package builderd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClassifyBuildFailureRequiresGuestCompletion(t *testing.T) {
	tests := []struct {
		name     string
		hostExit int
		marker   string
		want     string
	}{
		{"no host exit", -1, "", "FailureInfra"},
		{"firecracker configuration failure", 1, "", "FailureInfra"},
		{"clean host exit without guest", 0, "", "FailureInfra"},
		{"malformed guest result", 1, "{", "FailureInfra"},
		{"empty guest result", 1, "{}", "FailureInfra"},
		{"oom", 137, "", "FailureOOM"},
		{"timeout", 124, "", "FailureTimeout"},
		{"completed customer build failure", -9, `{"schema_version":1,"build_id":"build-1","exit_code":1}`, "FailureUserError"},
		{"guest timeout overrides host", 1, `{"schema_version":1,"build_id":"build-1","exit_code":124}`, "FailureTimeout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, "build-done.json"), []byte(tc.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, code, pkg := classifyBuildFailure(tc.hostExit, dir)
			if got != tc.want || code != "" || pkg != "" {
				t.Fatalf("class=%q code=%q pkg=%q, want %q", got, code, pkg, tc.want)
			}
		})
	}
}

func TestClassifyBuildFailurePreservesGuestExplanation(t *testing.T) {
	dir := t.TempDir()
	marker, err := json.Marshal(api.BuildDone{SchemaVersion: 1, BuildID: "build-1", ExitCode: 1, FailureClass: "FailureUserError", FailureCode: "dep_install_failed", FailurePkg: "npm"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-done.json"), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	class, code, pkg := classifyBuildFailure(-9, dir)
	if class != "FailureUserError" || code != "dep_install_failed" || pkg != "npm" {
		t.Fatalf("lost guest explanation: %q/%q/%q", class, code, pkg)
	}
}
