package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIFixtureRejectsInvalidInputsBeforeSourceAccess(t *testing.T) {
	for _, args := range [][]string{nil, {"export"}, {"build"}, {"build", "--config", "missing.json", "--output", filepath.Join(t.TempDir(), "new")}, {"build", "--config", "missing.json", "--output", "unused", "--database-url-env", "invalid-name"}} {
		if code := main.CmdDataAPIFixture(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	config := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(config, []byte(`{"version":1,"tables":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	existing := t.TempDir()
	if code := main.CmdDataAPIFixture([]string{"build", "--config", config, "--output", existing}); code == 0 {
		t.Fatal("existing output accepted")
	}
	t.Setenv("TEST_FIXTURE_SOURCE_MISSING", "")
	if code := main.CmdDataAPIFixture([]string{"build", "--config", config, "--output", filepath.Join(t.TempDir(), "new"), "--database-url-env", "TEST_FIXTURE_SOURCE_MISSING"}); code == 0 {
		t.Fatal("missing source accepted")
	}
}
