// adr: 20261112223000

package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// The 2026-11-15 agent eval replaced the owner's existing `retail-ui` from an
// unlinked checkout; the preview and the deploy now say when that happens.
func TestDeployTargetNamesAnUnlinkedExistingApp(t *testing.T) {
	if got := main.DeployTargetSummary(false, false); got != "new app" {
		t.Errorf("new app summary = %q", got)
	}
	if got := main.DeployTargetSummary(true, true); !strings.Contains(got, "linked to this directory") || strings.Contains(got, "not linked") {
		t.Errorf("linked summary = %q", got)
	}
	if got := main.DeployTargetSummary(true, false); !strings.Contains(got, "not linked") || !strings.Contains(got, "replaces its running release") {
		t.Errorf("unlinked summary = %q", got)
	}
	notice := main.UnlinkedExistingAppNotice("retail-ui")
	for _, want := range []string{"retail-ui already exists", "unused --name", "gregale link --app retail-ui"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q does not contain %q", notice, want)
		}
	}
}

func TestSourceLinkedToReadsTheCommittedManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if main.SourceLinkedTo(dir, "retail-ui") {
		t.Fatal("a directory with no link is linked")
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("project:\n  app: retail-ui\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !main.SourceLinkedTo(dir, "retail-ui") || main.SourceLinkedTo(dir, "retail-cart") {
		t.Fatal("gregale.yaml project.app does not link the directory to that app only")
	}
}
