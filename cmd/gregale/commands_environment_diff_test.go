package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentDiffUsesLinkedProject(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project_slug":"shop","from_environment":"staging","to_environment":"production","configuration":{"project_slug":"shop","from_environment":"staging","to_environment":"production","from_version":1,"to_version":2,"from_hash":"aaa","to_hash":"bbb","changes":[]},"workloads":[],"shared_resources":[],"generated_at":"2026-09-22T00:00:00Z"}`, http.StatusOK)
	root := t.TempDir()
	if _, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, ".gregale")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if code := environmentDiff([]string{"staging", "production"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "/v1/projects/shop/environments/production/diff"
	if f.sawMethod != http.MethodGet || f.sawPath != want || f.sawQuery != "from=staging" {
		t.Fatalf("request = %s %s?%s, want GET %s?from=staging", f.sawMethod, f.sawPath, f.sawQuery, want)
	}
}
