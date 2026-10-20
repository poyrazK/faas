package main_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestResolveWorkspaceContext(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	main.WriteFile(t, repo, "package.json", `{"workspaces":["apps/*"]}`)
	main.WriteFile(t, repo, "apps/api/package.json", `{"name":"api"}`)
	main.WriteFile(t, repo, "apps/web/Dockerfile", "FROM scratch\n")
	main.WriteFile(t, repo, "packages/shared/README.md", "shared\n")

	members, err := reposcan.WorkspaceMemberPaths(os.DirFS(repo))
	if err != nil {
		t.Fatalf("workspaceMemberPathsForTest: %v", err)
	}
	if want := []string{"apps/api", "apps/web"}; !reflect.DeepEqual(members, want) {
		t.Fatalf("workspace members = %v, want %v", members, want)
	}

	contextRoot, sourceRoot, ok, err := main.ResolveWorkspaceContext(repo, filepath.Join(repo, "apps/api"))
	if err != nil {
		t.Fatalf("resolveWorkspaceContext: %v", err)
	}
	if !ok {
		t.Fatal("resolveWorkspaceContext returned ok=false for workspace member")
	}
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	if contextRoot != filepath.Clean(absRepo) {
		t.Fatalf("contextRoot = %q, want %q", contextRoot, filepath.Clean(absRepo))
	}
	if sourceRoot != "apps/api" {
		t.Fatalf("sourceRoot = %q, want apps/api", sourceRoot)
	}

	if _, _, ok, err := main.ResolveWorkspaceContext(repo, filepath.Join(repo, "packages/shared")); err != nil {
		t.Fatalf("resolveWorkspaceContext(non-member): %v", err)
	} else if ok {
		t.Fatal("resolveWorkspaceContext returned ok=true for non-member")
	}
}
