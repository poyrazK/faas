package daemonunitspec

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// removedPostgresqlAliases are module arguments community.postgresql 5.0.0
// (deploy/ansible/requirements.yml) no longer accepts. rc.284's
// control-plane convergence failed on `postgresql_ext: db:` with "missing
// required arguments: login_db"; ansible-lint and syntax checks don't see it.
var removedPostgresqlAliases = map[string]string{
	"db": "login_db",
}

func TestAnsiblePostgresqlModulesUseCurrentArguments(t *testing.T) {
	root := filepath.Join(repoRoot(t), "deploy", "ansible")
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml")) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot(t), path)
		var doc any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			return nil
		}
		for _, call := range postgresqlModuleCalls(doc) {
			for arg := range call.args {
				if want, removed := removedPostgresqlAliases[arg]; removed {
					bad = append(bad, rel+": "+call.module+" passes `"+arg+"`; use `"+want+"`")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

func TestPostgresqlModuleCallsFindsTaskArguments(t *testing.T) {
	var doc any
	if err := yaml.Unmarshal([]byte(`
- name: t
  block:
    - community.postgresql.postgresql_ext:
        name: pg_stat_statements
        db: postgres
`), &doc); err != nil {
		t.Fatal(err)
	}
	calls := postgresqlModuleCalls(doc)
	if len(calls) != 1 || calls[0].module != "community.postgresql.postgresql_ext" {
		t.Fatalf("calls = %+v", calls)
	}
	if _, ok := calls[0].args["db"]; !ok {
		t.Fatalf("args = %v, want db", calls[0].args)
	}
}

type postgresqlModuleCall struct {
	module string
	args   map[string]any
}

// postgresqlModuleCalls returns every community.postgresql.* module call in a
// parsed YAML document: tasks, handlers and nested blocks.
func postgresqlModuleCalls(node any) []postgresqlModuleCall {
	var out []postgresqlModuleCall
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.HasPrefix(key, "community.postgresql.") {
				if args, ok := child.(map[string]any); ok {
					out = append(out, postgresqlModuleCall{module: key, args: args})
				}
				continue
			}
			out = append(out, postgresqlModuleCalls(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, postgresqlModuleCalls(child)...)
		}
	}
	return out
}
