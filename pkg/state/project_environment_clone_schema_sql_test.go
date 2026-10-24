package state_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The Postgres coverage test needs a migrated database, which ci-light does
// not start. schema.sql is the same migrated schema, so a registry entry that
// names a column no migration created (#5275 renamed deploy_tokens.token_sha256)
// fails here without one.
func TestCloneSchemaRegistryMatchesSchemaSQL(t *testing.T) {
	raw, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	report, err := state.CloneSchemaCoverage(schemaSQLTables(t, string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// Copy-strategy blockers are the registry's own verdicts; only table and
	// column mismatches mean it disagrees with the schema.
	var drift []state.ProjectEnvironmentCloneCoverageBlocker
	for _, blocker := range report.Blockers {
		switch blocker.Code {
		case "unregistered_table", "missing_table", "unregistered_column", "missing_column":
			drift = append(drift, blocker)
		}
	}
	if len(drift) != 0 {
		t.Fatalf("clone schema registry differs from schema.sql: %+v", drift)
	}
}

var (
	schemaSQLTable     = regexp.MustCompile(`(?m)^CREATE (?:UNLOGGED )?TABLE public\.(\S+) \($`)
	schemaSQLPartition = regexp.MustCompile(`(?m)ATTACH PARTITION public\.(\S+) `)
	schemaSQLColumn    = regexp.MustCompile(`^    ("[^"]+"|[a-z_][a-z0-9_]*) `)
)

// schemaSQLTables mirrors the coverage query: ordinary and partitioned tables
// in the public schema, without partitions, with their live columns.
func schemaSQLTables(t *testing.T, schema string) []state.ProjectEnvironmentCloneSchemaPolicy {
	t.Helper()
	partitions := map[string]bool{}
	for _, m := range schemaSQLPartition.FindAllStringSubmatch(schema, -1) {
		partitions[m[1]] = true
	}
	var tables []state.ProjectEnvironmentCloneSchemaPolicy
	for _, loc := range schemaSQLTable.FindAllStringSubmatchIndex(schema, -1) {
		name := schema[loc[2]:loc[3]]
		if partitions[name] {
			continue
		}
		body := schema[loc[1]:]
		body = body[:strings.Index(body, "\n)")]
		table := state.ProjectEnvironmentCloneSchemaPolicy{TableName: name}
		for _, line := range strings.Split(body, "\n") {
			if m := schemaSQLColumn.FindStringSubmatch(line); m != nil {
				table.Columns = append(table.Columns, strings.Trim(m[1], `"`))
			}
		}
		tables = append(tables, table)
	}
	if len(tables) == 0 {
		t.Fatal("schema.sql has no tables")
	}
	return tables
}
