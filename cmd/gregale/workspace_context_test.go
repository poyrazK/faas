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
	const fromRoot = "building apps/svc from the repository root"
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
			wantRepo:   true,
			wantReason: fromRoot + ": its Dockerfile copies apps/svc, packages, pnpm-lock.yaml, which exist only there",
		},
		{
			name: "member and root sources mixed",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM node\nCOPY server.js pnpm-lock.yaml ./\n",
				"apps/svc/server.js":  "x",
				"pnpm-lock.yaml":      "x",
			},
			wantRepo:   true,
			wantReason: fromRoot + ": its Dockerfile copies pnpm-lock.yaml, which exists only there",
		},
		{
			name: "a comment inside a continuation does not hide a root source",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM node\nCOPY server.js \\\n  # root lockfile\n\n  pnpm-lock.yaml ./\n",
				"apps/svc/server.js":  "x",
				"pnpm-lock.yaml":      "x",
			},
			wantRepo:   true,
			wantReason: fromRoot + ": its Dockerfile copies pnpm-lock.yaml, which exists only there",
		},
		{
			name: "heredoc bodies are not instructions",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM x\nRUN <<EOF\nCOPY pnpm-lock.yaml ./\nEOF\nCOPY server.js ./\n",
				"apps/svc/server.js":  "x",
				"pnpm-lock.yaml":      "x",
			},
			wantReason: "building apps/svc from its own directory: its Dockerfile copies server.js, which exists there and not at the repository root",
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
			name: "long source lists are counted",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM x\nCOPY a b c d e ./\n",
				"apps/svc/a":          "x", "apps/svc/b": "x", "apps/svc/c": "x", "apps/svc/d": "x", "apps/svc/e": "x",
			},
			wantReason: "building apps/svc from its own directory: its Dockerfile copies a, b, c and 2 more, which exist there and not at the repository root",
		},
		{
			name: "sources found in both places decide nothing",
			files: map[string]string{
				"apps/svc/Dockerfile": "FROM x\nCOPY . .\nCOPY src ./src\n",
				"apps/svc/src/a":      "x",
				"src/a":               "x",
			},
			wantRepo:   true,
			wantReason: fromRoot + ", as a workspace member: its Dockerfile copies nothing that exists only in apps/svc",
		},
		{
			name:       "member without a Dockerfile",
			files:      map[string]string{"apps/svc/package.json": "{}", "apps/svc/server.js": "x"},
			wantRepo:   true,
			wantReason: fromRoot + ", as a workspace member",
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
			contextRoot, sourceRoot, ok, reason, err := main.ResolveWorkspaceContextReason(repo, filepath.Join(repo, "apps/svc"), true, false)
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

// A HEAD deploy decides from committed files, not from local edits or
// untracked files it will not upload.
func TestResolveWorkspaceContextReadsTheShippedTree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGitCmdOrSkip(t, repo, "init", "-q")
	runGitCmdOrSkip(t, repo, "config", "user.name", "Test")
	runGitCmdOrSkip(t, repo, "config", "user.email", "test@example.com")
	main.WriteFile(t, repo, "package.json", `{"workspaces":["apps/*"]}`)
	main.WriteFile(t, repo, "apps/svc/Dockerfile", "FROM x\nCOPY pom.xml *.xml ./\n")
	main.WriteFile(t, repo, "apps/svc/pom.xml", "x")
	runGitCmdOrSkip(t, repo, "add", ".")
	runGitCmdOrSkip(t, repo, "commit", "-q", "-m", "member service")
	// Uncommitted: the Dockerfile now copies a root file that is not tracked.
	main.WriteFile(t, repo, "apps/svc/Dockerfile", "FROM x\nCOPY pom.xml yarn.lock ./\n")
	main.WriteFile(t, repo, "yarn.lock", "x")

	dir := filepath.Join(repo, "apps/svc")
	_, _, ok, reason, err := main.ResolveWorkspaceContextReason(repo, dir, false, true)
	if err != nil || ok {
		t.Fatalf("HEAD: ok=%v reason=%q err=%v, want the member context", ok, reason, err)
	}
	if want := "building apps/svc from its own directory: its Dockerfile copies *.xml, pom.xml, which exist there and not at the repository root"; reason != want {
		t.Fatalf("HEAD reason = %q, want %q", reason, want)
	}
	if _, _, ok, reason, err := main.ResolveWorkspaceContextReason(repo, dir, true, false); err != nil || !ok {
		t.Fatalf("worktree: ok=%v reason=%q err=%v, want the repository context", ok, reason, err)
	}
}
