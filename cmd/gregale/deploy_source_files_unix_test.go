//go:build unix

package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestDeploySourceFilesSymlinkParity(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "src/app.js": "resolved source"})
	for link, target := range map[string]string{"entry.js": "src/app.js", "alias": "src"} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"worktree", "head"} {
		if source == "head" {
			initSourceFilesGit(t, dir)
		}
		report, archive, err := main.ExplainDeploySourceForTest(t, dir, source)
		if err != nil {
			t.Fatal(err)
		}
		assertSourceFileArchive(t, report, archive)
		rows := sourceFileRows(report)
		if !rows["entry.js"].Included || rows["entry.js"].Bytes != int64(len("resolved source")) || !rows["alias/app.js"].Included {
			t.Fatalf("symlink payload missing: %v", report)
		}
		if source == "worktree" && !rows["entry.js"].Symlink {
			t.Fatal("worktree symlink was not identified")
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside.js")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := main.ExplainDeploySourceForTest(t, dir, "worktree"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping symlink should retain the packer's refusal: %v", err)
	}
}
