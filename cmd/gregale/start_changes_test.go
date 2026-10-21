package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStartProjectSourceComparisonIncludesWorkingFilesAndExclusions(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	root := t.TempDir()
	writeFile(t, root, "app.js", "before")
	writeFile(t, root, "remove.txt", "gone")
	writeFile(t, root, "selected.env", "PRIVATE=original-secret")
	writeFile(t, root, ".gregaleignore", "ignored.txt\n")
	writeFile(t, root, "ignored.txt", "ignored-before")
	writeFile(t, root, "node_modules/dependency.js", "generated-before")
	secret := filepath.Join(root, "selected.env")
	before, err := readStartSourceSnapshot(t.Context(), root, 1<<20, secret)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "app.js", "after!") // Same size and mtime must still be detected.
	if err := os.Chtimes(filepath.Join(root, "app.js"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "new.txt", "uncommitted-customer-content")
	writeFile(t, root, "selected.env", "PRIVATE=replacement-secret")
	writeFile(t, root, "ignored.txt", "ignored-after")
	writeFile(t, root, "node_modules/dependency.js", "generated-after")
	if err := os.Remove(filepath.Join(root, "remove.txt")); err != nil {
		t.Fatal(err)
	}
	after, err := readStartSourceSnapshot(t.Context(), root, 1<<20, secret)
	if err != nil {
		t.Fatal(err)
	}
	want := []startSourceChange{{"app.js", "modified"}, {"new.txt", "added"}, {"remove.txt", "deleted"}}
	if got := startSourceChanges(before, after); !reflect.DeepEqual(got, want) {
		t.Fatalf("source comparison=%v, want %v", got, want)
	}
	renderStartSourceChanges(before, after)
	for _, forbidden := range []string{"uncommitted-customer-content", "replacement-secret", "selected.env", "node_modules", "ignored.txt"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("source review exposed excluded content or file %q: %s", forbidden, stdout)
		}
	}
}

func TestStartProjectSourceReviewBoundsAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app.js", "too large")
	if _, err := readStartSourceSnapshot(t.Context(), root, 2); err == nil {
		t.Fatal("source review exceeded its byte budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readStartSourceSnapshot(ctx, root, 1<<20); err == nil {
		t.Fatal("canceled source review continued")
	}
	if err := os.Symlink(filepath.Join(root, "app.js"), filepath.Join(root, "linked.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readStartSourceSnapshot(t.Context(), root, 1<<20); err == nil {
		t.Fatal("source review followed a symlink")
	}
}

func TestStartProjectSourceReviewRejectsEditsAfterApproval(t *testing.T) {
	startTestEnvironment(t)
	root := startTestSource(t)
	runner := startTestRunner(t, root, "")
	if err := runner.prepareSourceReview(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "extra.txt", "edited after preview")
	if err := runner.verifyReviewedSource(runner.ctx); err == nil {
		t.Fatal("source changed after preview without requiring a new review")
	}
}

func TestStartProjectSourceReviewIncludesSharedWorkspaceContext(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	root := resolvedTempDir(t)
	mustGit(t, root, "init", "--quiet")
	writeFile(t, root, "package.json", `{"workspaces":["apps/*"]}`)
	writeFile(t, root, "apps/api/package.json", `{"name":"api"}`)
	writeFile(t, root, "apps/api/index.js", "before")
	writeFile(t, root, "packages/shared/util.js", "before")
	withCwd(t, filepath.Join(root, "apps/api"))
	runner := startTestRunner(t, filepath.Join(root, "apps/api"), "")
	before, err := runner.snapshotSource(runner.ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "packages/shared/util.js", "after!")
	writeFile(t, root, "apps/api/index.js", "after!")
	runner.sourceBaseline, runner.projectUpdates = before, true
	if err := runner.prepareSourceReview(); err != nil {
		t.Fatal(err)
	}
	want := []startSourceChange{{"apps/api/index.js", "modified"}, {"packages/shared/util.js", "modified"}}
	if got := startSourceChanges(before, runner.reviewedSource); !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace review missed a packaged dependency: got=%v want=%v", got, want)
	}
	if !strings.Contains(stdout.String(), "Build context") || !strings.Contains(stdout.String(), `modified "packages/shared/util.js"`) {
		t.Fatalf("workspace source review not shown: %s", stdout)
	}
}
