package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIMigrationPreviewRejectsInputsBeforeServices(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"apply"}, {"preview", "--baseline", "missing.json", "--from-version", "0"}, {"preview", "--baseline", "missing.json", "--from-version", "-1"}, {"preview", "--baseline", "missing.json", "--from-version", "10000"}, {"preview", "--baseline", "missing.json", "--fixture", "older.sql"}, {"preview"}, {"preview", "--baseline", "missing.json", "--directory", project}, {"preview", "--baseline", "missing.json", "--scenario", "alone"}, {"preview", "--baseline", "missing.json", "unexpected"}} {
		if code := main.CmdDataAPIMigration(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	baseline := filepath.Join(project, "baseline.json")
	if err := os.WriteFile(baseline, main.DataAPIContractFixture(t, main.DataAPIDiffFixture()), 0600); err != nil {
		t.Fatal(err)
	}
	deps := filepath.Join(project, "dependencies.json")
	if err := os.WriteFile(deps, []byte(`{"version":1,"consumers":[{"kind":"typo"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdDataAPIMigration([]string{"preview", "--directory", project, "--baseline", "baseline.json", "--dependencies", "dependencies.json"}); code == 0 {
		t.Fatal("invalid dependencies accepted")
	}
}
