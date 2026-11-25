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

// A member whose own Dockerfile was written for its directory builds from that
// directory; one that copies root files keeps the repository (ADR-144).
func TestResolveWorkspaceContextMemberDockerfile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		files      map[string]string
		wantRepo   bool
		wantReason string
	}{
		{
			name: "maven service copies its own files",
			files: map[string]string{
				"apps/svc/Dockerfile":              "FROM x AS build\n# deps\nCOPY .mvn .mvn\nCOPY mvnw pom.xml ./\nCOPY ./src ./src\nRUN ./mvnw \\\n  package\nFROM y\nCOPY --from=build /out/app.jar /app.jar\n",
				"apps/svc/.mvn/wrapper.properties": "x",
				"apps/svc/mvnw":                    "x",
				"apps/svc/pom.xml":                 "x",
				"apps/svc/src/A.java":              "x",
				"src/README.md":                    "root src",
			},
			wantReason: "building apps/svc from its own directory: its Dockerfile copies .mvn, mvnw, pom.xml, which exist there and not at the repository root",
		},
		{
			name: "turborepo Dockerfile copies the lockfile and packages",
			files: map[string]string{
				"apps/svc/Dockerfile":      "FROM node\nCOPY package.json pnpm-lock.yaml ./\nCOPY packages ./packages\nCOPY apps/svc ./apps/svc\n",
				"apps/svc/package.json":    "{}",
				"pnpm-lock.yaml":           "x",
				"packages/shared/index.js": "x",
			},
			wantRepo: true,
		},
		{
			name: "member and root sources mixed",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM node\nCOPY server.js pnpm-lock.yaml ./\n",
				"apps/svc/server.js":  "x",
				"pnpm-lock.yaml":      "x",
			},
			wantRepo: true,
		},
		{
			name: "JSON form, ADD and globs",
			files: map[string]string{
				"apps/svc/Dockerfile":    "FROM x\nADD --chown=1:1 [\"go.mod\", \"go.sum\", \"./\"]\nCOPY *.csproj ./\nCOPY https://example.com/x /x\nCOPY $APP_DIR /app\n",
				"apps/svc/go.mod":        "module x",
				"apps/svc/go.sum":        "",
				"apps/svc/worker.csproj": "x",
			},
			wantReason: "building apps/svc from its own directory: its Dockerfile copies *.csproj, go.mod, go.sum, which exist there and not at the repository root",
		},
		{
			name: "sources found in both places decide nothing",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM x\nCOPY . .\nCOPY src ./src\n",
				"apps/svc/src/a":      "x",
				"src/a":               "x",
			},
			wantRepo: true,
		},
		{
			name:     "member without a Dockerfile",
			files:    map[string]string{"apps/svc/package.json": "{}", "apps/svc/server.js": "x"},
			wantRepo: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := t.TempDir()
			main.WriteFile(t, repo, "package.json", `{"workspaces":["apps/*"]}`)
			for name, body := range tc.files {
				main.WriteFile(t, repo, name, body)
			}
			contextRoot, sourceRoot, ok, reason, err := main.ResolveWorkspaceContextReason(repo, filepath.Join(repo, "apps/svc"))
			if err != nil {
				t.Fatal(err)
			}
			if ok != tc.wantRepo || reason != tc.wantReason {
				t.Fatalf("ok=%v reason=%q, want ok=%v reason=%q", ok, reason, tc.wantRepo, tc.wantReason)
			}
			if !ok && (contextRoot != "" || sourceRoot != "") {
				t.Fatalf("member context returned contextRoot=%q sourceRoot=%q", contextRoot, sourceRoot)
			}
			if ok && sourceRoot != "apps/svc" {
				t.Fatalf("sourceRoot = %q", sourceRoot)
			}
		})
	}
}
