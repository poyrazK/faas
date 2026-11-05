package faasschema_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	faasschema "github.com/onebox-faas/faas"
)

var (
	createTablePattern = regexp.MustCompile(`(?m)^CREATE TABLE public\.([a-z0-9_]+) \(\n((?:.*\n)*?)\)`)
	partitionPattern   = regexp.MustCompile(`ATTACH PARTITION public\.([a-z0-9_]+) `)
	// Column lines are indented lower-case names; CONSTRAINT lines are not.
	columnPattern = regexp.MustCompile(`^\s+"?([a-z0-9_]+)"? `)
)

// A table a migration adds must be classified for project environment clones
// in the same change, with the same columns. An unregistered table makes clone
// schema coverage unknown, which fails clone admission and the managed
// PostgreSQL cutover tests for every later PR. Attached partitions inherit
// their parent's classification.
func TestCanonicalSchemaTablesAreCloneClassified(t *testing.T) {
	raw, err := os.ReadFile("pkg/state/project_environment_clone_schema_registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var policies []struct {
		Table   string   `json:"table"`
		Columns []string `json:"columns"`
	}
	if err := json.Unmarshal(raw, &policies); err != nil {
		t.Fatal(err)
	}
	registered := make(map[string][]string, len(policies))
	for _, policy := range policies {
		registered[policy.Table] = policy.Columns
	}
	schema := faasschema.CanonicalSQL()
	partitions := map[string]bool{}
	for _, match := range partitionPattern.FindAllStringSubmatch(schema, -1) {
		partitions[match[1]] = true
	}
	tables := createTablePattern.FindAllStringSubmatch(schema, -1)
	if len(tables) < 100 {
		t.Fatalf("parsed %d tables from the canonical schema; the pattern no longer matches pg_dump output", len(tables))
	}
	for _, match := range tables {
		table := match[1]
		if partitions[table] {
			continue
		}
		want, ok := registered[table]
		if !ok {
			t.Errorf("%s is not in pkg/state/project_environment_clone_schema_registry.json", table)
			continue
		}
		var columns []string
		for _, line := range strings.Split(match[2], "\n") {
			if column := columnPattern.FindStringSubmatch(line); column != nil {
				columns = append(columns, column[1])
			}
		}
		slices.Sort(columns)
		want = slices.Sorted(slices.Values(want))
		if !slices.Equal(columns, want) {
			t.Errorf("%s registry columns %v; the schema has %v", table, want, columns)
		}
	}
}
