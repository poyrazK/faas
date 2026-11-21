package main_test

import (
	main "github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskPipelineManifestRequiresManagedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(`{"workflow_id":"batch","version":"v1","batch":{"name":"report","version":1,"items":[{"label":"one"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := main.Run([]string{"sandbox", "workflow", "run", "--manifest", path, "--dry-run"}); code != 1 {
		t.Fatal("local mode accepted task pipeline", code)
	}
	if code := main.Run([]string{"sandbox", "workflow", "run", "--manifest", path, "--managed", "--dry-run"}); code != 0 {
		t.Fatal("shape-only preview", code)
	}
}

func TestPipelineWatchRejectsInvalidCursorBeforeAuthentication(t *testing.T) {
	for _, cursor := range []string{"-1", "not-a-cursor"} {
		if code := main.Run([]string{"sandbox", "workflow", "watch", "pipeline-id", "--after", cursor}); code != 1 {
			t.Fatal("invalid watch cursor accepted", cursor, code)
		}
	}
}
