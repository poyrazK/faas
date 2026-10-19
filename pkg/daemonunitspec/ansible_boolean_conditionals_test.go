package daemonunitspec

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// stringDefaultCondition matches a condition whose value is the string from an
// empty-string default filter. Ansible 2.19+ fails such a when: with
// "Conditionals must have a boolean result": rc.276's control-plane
// convergence stopped on faas_postgres_standby_address in the postgres role.
var stringDefaultCondition = regexp.MustCompile(`\|\s*default\(\s*(''|"")\s*(,\s*(true|True))?\s*\)\s*$`)

// booleanCondition matches what turns a value into a boolean: a comparison,
// a test, a boolean operator or filter.
var booleanCondition = regexp.MustCompile(`==|!=|<=|>=|<|>|\bis\b|\bin\b|\bnot\b|\band\b|\bor\b|\|\s*(bool|length|int|ternary)\b`)

func TestAnsibleConditionsAreBoolean(t *testing.T) {
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
		var doc any
		if yaml.Unmarshal(body, &doc) != nil {
			return nil // templates and non-YAML files under deploy/ansible
		}
		rel, _ := filepath.Rel(repoRoot(t), path)
		for _, cond := range collectWhen(doc) {
			if stringDefaultCondition.MatchString(cond) && !booleanCondition.MatchString(cond) {
				bad = append(bad, rel+": when: "+cond)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, b := range bad {
		t.Errorf("%s evaluates to a string; write `(... | default('')) | length > 0`", b)
	}
}

func TestStringDefaultConditionRule(t *testing.T) {
	for _, tc := range []struct {
		cond string
		bad  bool
	}{
		{"faas_postgres_standby_address | default('')", true},
		{`faas_x | default("")`, true},
		{"faas_x | default('', true)", true},
		{"(faas_postgres_standby_address | default('')) | length > 0", false},
		{"not (faas_postgres_standby_address | default(''))", false},
		{"faas_x | default('') != ''", false},
		{"faas_x | default(false) | bool", false},
		{"faas_x is defined", false},
	} {
		got := stringDefaultCondition.MatchString(tc.cond) && !booleanCondition.MatchString(tc.cond)
		if got != tc.bad {
			t.Errorf("%q: flagged = %v, want %v", tc.cond, got, tc.bad)
		}
	}
}

// collectWhen returns every when: condition anywhere in a parsed YAML
// document: tasks, handlers, blocks, rescue/always and play-level roles.
func collectWhen(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "when" {
				switch w := child.(type) {
				case string:
					out = append(out, w)
				case []any:
					for _, c := range w {
						if s, ok := c.(string); ok {
							out = append(out, s)
						}
					}
				}
				continue
			}
			out = append(out, collectWhen(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, collectWhen(child)...)
		}
	}
	return out
}
