package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// TestTemplateReadmesMatchMaterializationAndCLIContract keeps the embedded
// starter instructions executable as the template set and CLI evolve.
// In particular, it catches commands that omit the required app slug,
// references to the removed `gregale curl` helper, and claims about files
// that Materialize does not actually write.
func TestTemplateReadmesMatchMaterializationAndCLIContract(t *testing.T) {
	for _, name := range templates.Names {
		name := name
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), name)
			if err := templates.Materialize(name, dest); err != nil {
				t.Fatalf("materialize %q: %v", name, err)
			}
			readmeBytes, err := os.ReadFile(filepath.Join(dest, "README.md"))
			if err != nil {
				t.Fatalf("read README: %v", err)
			}
			readme := string(readmeBytes)

			if strings.Contains(readme, "gregale curl") {
				t.Errorf("README references removed command `gregale curl`")
			}
			for _, line := range strings.Split(readme, "\n") {
				idx := strings.Index(line, "gregale open")
				if idx >= 0 {
					command := strings.Fields(line[idx:])
					if len(command) < 3 || command[0] != "gregale" || command[1] != "open" || command[2] != "<slug>" {
						t.Errorf("open example must pass <slug>: %q", strings.TrimSpace(line))
					}
				}
				if strings.Contains(line, "gregale deploy --template") && !strings.Contains(line, "--name <slug>") {
					t.Errorf("template deploy example must pass --name <slug>: %q", strings.TrimSpace(line))
				}
			}

			if name == "hello-go" {
				mod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
				if err != nil {
					t.Fatalf("hello-go materialization missing go.mod: %v", err)
				}
				if !strings.Contains(string(mod), "go 1.24\n") {
					t.Errorf("hello-go go.mod missing Go 1.24 directive: %q", mod)
				}
				for _, want := range []string{"go.mod", "go 1.24"} {
					if !strings.Contains(readme, want) {
						t.Errorf("hello-go README missing %q", want)
					}
				}
			}
		})
	}
}

func TestEditableHelloReadmesUseInitializedSource(t *testing.T) {
	for _, name := range []string{"hello-go", "hello-node", "hello-python"} {
		name := name
		t.Run(name, func(t *testing.T) {
			readmeBytes, err := fs.ReadFile(templates.FS, filepath.ToSlash(filepath.Join(name, "README.md")))
			if err != nil {
				t.Fatalf("read README: %v", err)
			}
			readme := string(readmeBytes)
			if !strings.Contains(readme, "gregale init --template "+name+" --path "+name) {
				t.Errorf("README must initialize a local source directory before editing")
			}
			if !strings.Contains(readme, "gregale deploy --name <slug>") {
				t.Errorf("README must redeploy the initialized source with gregale deploy --name <slug>")
			}
			editSection := readme
			if idx := strings.Index(readme, "## Edit and re-deploy"); idx >= 0 {
				editSection = readme[idx:]
			}
			if strings.Contains(editSection, "gregale deploy --template "+name) {
				t.Errorf("README edit instructions must not re-materialize --template %s", name)
			}
		})
	}
}
