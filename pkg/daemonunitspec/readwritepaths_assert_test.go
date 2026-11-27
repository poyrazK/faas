package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The compute role asserts every ReadWritePaths= target of imaged, vmmd and
// builderd exists. systemd's "-" (ignore if missing) and "+" prefixes are not
// part of the path, so the assert must strip them before it stats: #7747
// listed -/srv/fc/run-environments and the literal "-/srv/..." stat failed
// the rc.366 compute rollout on a node where the directory existed.
func TestComputeReadWritePathsAssertStripsSystemdPrefixes(t *testing.T) {
	const name = "compute_only_service — assert ReadWritePaths= targets exist per unit (spec §11)"
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "compute_only_service", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []struct {
		Name string `yaml:"name"`
		Vars struct {
			Paths string `yaml:"faas_rwp_paths_flat"`
		} `yaml:"vars"`
	}
	if err := yaml.Unmarshal(raw, &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Name != name {
			continue
		}
		pipeline := task.Vars.Paths
		split := strings.Index(pipeline, "map('split')")
		strip := strings.Index(pipeline, "map('regex_replace', '^[-+]', '')")
		if split < 0 || strip < split {
			t.Fatalf("ReadWritePaths assert must strip systemd's -/+ prefixes after splitting; pipeline:\n%s", pipeline)
		}
		return
	}
	t.Fatalf("task %q not found", name)
}
