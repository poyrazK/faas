// adr: 20261112223000

package main_test

import (
	"os"
	"path/filepath"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Docker always sends the Dockerfile and .dockerignore, whatever .dockerignore
// says. The 2026-11-15 agent eval's builds failed because a root .dockerignore
// listing `Dockerfile` left it out of the upload.
func TestRootDockerignoreKeepsTheDockerfile(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"Dockerfile":    "FROM eclipse-temurin:21\nCOPY target/app.jar /app.jar\n",
		".dockerignore": "Dockerfile\n.dockerignore\n*.md\nsecrets/\n",
		"README.md":     "x",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		rel      string
		dir, out bool
	}{
		{"Dockerfile", false, false},
		{".dockerignore", false, false},
		{"README.md", false, true},
		{"secrets", true, true},
		{"target", true, false},
	} {
		if got := main.PackExcludes(root, true, tc.rel, tc.dir); got != tc.out {
			t.Errorf("exclude(%s) = %v, want %v", tc.rel, got, tc.out)
		}
	}
}
