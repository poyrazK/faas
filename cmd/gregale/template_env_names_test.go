package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// TestTemplatesDoNotEnumerateEnvironment keeps starter apps from publishing
// their environment variable names. A deployed template answers on a public
// URL, and secret key names (STRIPE_SECRET_KEY, DATABASE_URL, ...) reveal a
// customer's integrations. The hello templates used to list them on `/`.
func TestTemplatesDoNotEnumerateEnvironment(t *testing.T) {
	enumerations := []string{
		"os.Environ()",             // Go
		"Object.keys(process.env)", // Node
		"Object.entries(process.env)",
		"os.environ.keys()", // Python
		"os.environ.items()",
		"dict(os.environ)",
	}
	for _, name := range templates.Names {
		name := name
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), name)
			if err := templates.Materialize(name, dest); err != nil {
				t.Fatalf("materialize %q: %v", name, err)
			}
			err := filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				switch filepath.Ext(path) {
				case ".go", ".js", ".mjs", ".ts", ".py":
				default:
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				body := string(raw)
				for _, pattern := range enumerations {
					if strings.Contains(body, pattern) {
						rel, _ := filepath.Rel(dest, path)
						t.Errorf("%s enumerates the environment (%s); a public response must not list env var names", rel, pattern)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk %q: %v", name, err)
			}
		})
	}
}
