package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo builds a repository whose release tag v0.1.0 has Doc{a string},
// and whose base commit (HEAD) applies baseDoc on top.
func gitRepo(t *testing.T, baseDoc string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeDoc(t, repo, "type Doc struct {\n\tA string `json:\"a\"`\n}\n")
	run("add", ".")
	run("commit", "-q", "-m", "release")
	run("tag", "v0.1.0")
	writeDoc(t, repo, baseDoc)
	run("commit", "-q", "--allow-empty", "-am", "base")
	return repo
}

func writeDoc(t *testing.T, repo, body string) {
	t.Helper()
	path := filepath.Join(repo, "pkg/api/doc.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package api\n\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunClassifiesAgainstTheRelease(t *testing.T) {
	const released = "type Doc struct {\n\tA string `json:\"a\"`\n}\n"
	const brokenOnBase = "type Doc struct {\n\tA int `json:\"a\"`\n}\n"
	tests := []struct {
		name  string
		base  string
		head  string
		allow string
		want  int
	}{
		{name: "compatible addition", base: released, head: "type Doc struct {\n\tA string `json:\"a\"`\n\tB string `json:\"b,omitempty\"`\n}\n", want: 0},
		{name: "introduced break fails", base: released, head: "type Doc struct {\n\tA string `json:\"a\"`\n\tB string `json:\"b\"`\n}\n", want: 1},
		{name: "a break the base already has does not fail", base: brokenOnBase, head: brokenOnBase, want: 0},
		{name: "a new break on top of an old one fails", base: brokenOnBase, head: "type Doc struct {\n\tA int `json:\"a\"`\n\tB string `json:\"b\"`\n}\n", want: 1},
		{
			name:  "an allowlisted break passes",
			base:  released,
			head:  "type Doc struct {\n\tA string `json:\"a\"`\n\tB string `json:\"b\"`\n}\n",
			allow: "guest:Doc.b:not-omitempty  # only the new builder reads it\n",
			want:  0,
		},
		{name: "an allowlist entry needs a reason", base: released, head: released, allow: "guest:Doc.b:not-omitempty\n", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := gitRepo(t, tt.base)
			writeDoc(t, repo, tt.head)
			allow := filepath.Join(repo, "allow.txt")
			if tt.allow != "" {
				if err := os.WriteFile(allow, []byte(tt.allow), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := run([]string{"-repo", repo, "-base", "HEAD", "-check", "guest", "-guest-types", "Doc", "-allow", "allow.txt"}, io.Discard)
			if got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveReleaseFallsBackToBase(t *testing.T) {
	repo := gitRepo(t, "type Doc struct{}\n")
	head, err := git(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveRelease(io.Discard, repo, "auto", head); err != nil || got != "v0.1.0" {
		t.Fatalf("auto = %q, %v; want v0.1.0", got, err)
	}
	if _, err := git(repo, "tag", "-d", "v0.1.0"); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveRelease(io.Discard, repo, "auto", head); err != nil || got != head {
		t.Fatalf("auto without tags = %q, %v; want the base %s", got, err, head)
	}
}
