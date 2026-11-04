// adr: 20261026110240
package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeWritesAgentsGuideForEveryTemplate(t *testing.T) {
	for _, name := range Names {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			if err := Materialize(name, dest); err != nil {
				t.Fatal(err)
			}
			guide, err := os.ReadFile(filepath.Join(dest, "AGENTS.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"`" + name + "` starter", "--non-interactive --json", "resume_command", "FAAS_TOKEN", "https://gregale.dev/docs/agents"} {
				if !strings.Contains(string(guide), want) {
					t.Errorf("AGENTS.md lacks %q", want)
				}
			}
			if strings.Contains(string(guide), "{{") {
				t.Error("AGENTS.md has an unrendered template action")
			}
		})
	}
}

func TestMaterializeKeepsAnExistingAgentsGuide(t *testing.T) {
	dest := t.TempDir()
	path := filepath.Join(dest, "AGENTS.md")
	if err := os.WriteFile(path, []byte("customer instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Materialize("hello-node", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "customer instructions\n" {
		t.Fatalf("existing AGENTS.md was replaced: %q", got)
	}
}
