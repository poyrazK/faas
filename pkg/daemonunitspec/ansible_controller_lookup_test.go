package daemonunitspec

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fileLookupPattern = regexp.MustCompile(`lookup\(\s*['"](?:ansible\.builtin\.)?file['"]\s*,\s*([^)]+)\)`)

// A file lookup runs on the Ansible controller. Inside a role it may read the
// role's own files; it must never read a host path such as a secret under
// /etc, because the controller is a separate deployment runner, not the host
// being configured. The Grafana reload handler did exactly that and failed on
// the first bootstrap run from outside the control plane.
func TestAnsibleRoleFileLookupsReadOnlyRoleFiles(t *testing.T) {
	root := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yml") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range fileLookupPattern.FindAllStringSubmatch(string(body), -1) {
			checked++
			if !strings.HasPrefix(strings.TrimSpace(m[1]), "role_path") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s: file lookup of %s reads the controller, not a role file; slurp it on the target host", rel, strings.TrimSpace(m[1]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no file lookups found; the pattern no longer matches the roles")
	}
}
