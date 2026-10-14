package debugger

// adr: 958 — database dependencies are broken down by normalized query.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestQueryPatternGroupsEquivalentStatements(t *testing.T) {
	for name, group := range map[string][]string{
		"literals": {
			"SELECT * FROM orders WHERE id = 42",
			"SELECT * FROM orders WHERE id = 43",
		},
		"strings": {
			"SELECT * FROM users WHERE email = 'a@example.com'",
			"SELECT * FROM users WHERE email = 'b@example.com'",
		},
		"in lists": {
			"SELECT * FROM items WHERE id IN (1, 2, 3)",
			"SELECT * FROM items WHERE id IN (7)",
			"SELECT * FROM items WHERE id IN ($1, $2)",
		},
		"values rows": {
			"INSERT INTO events (a, b) VALUES (1, 'x')",
			"INSERT INTO events (a, b) VALUES (1, 'x'), (2, 'y'), (3, 'z')",
		},
		"placeholders": {
			"UPDATE carts SET total = $1 WHERE id = $2",
			"UPDATE carts SET total = :total WHERE id = :id",
			"UPDATE  carts SET total = ?\n WHERE id = ?",
		},
	} {
		t.Run(name, func(t *testing.T) {
			first := QueryPattern(group[0])
			for _, statement := range group[1:] {
				if got := QueryPattern(statement); got != first {
					t.Fatalf("QueryPattern(%q) = %q, want %q", statement, got, first)
				}
			}
		})
	}
}

func TestQueryPatternKeepsDistinctQueriesApart(t *testing.T) {
	a := QueryPattern("SELECT * FROM orders WHERE id = 1")
	b := QueryPattern("SELECT * FROM orders WHERE customer_id = 1")
	if a == b || a == "" {
		t.Fatalf("patterns %q and %q must differ", a, b)
	}
	// Postgres casts are not placeholders.
	if got := QueryPattern("SELECT id::text FROM t WHERE id = :id"); got != "SELECT id::text FROM t WHERE id = ?" {
		t.Fatalf("cast pattern = %q", got)
	}
	if QueryPattern("SELECT id::text FROM t") == QueryPattern("SELECT id::int FROM t") {
		t.Fatal("different casts merged into one pattern")
	}
	if strings.Contains(QueryPattern("SELECT * FROM users WHERE email = 'alice@example.com'"), "alice") {
		t.Fatal("pattern leaked a literal")
	}
	if QueryPattern("   ") != "" {
		t.Fatal("empty statement must have no pattern")
	}
	if got := QueryPattern("SELECT " + strings.Repeat("col, ", 200) + "x FROM t"); len(got) > DebugQueryPatternMaxBytes {
		t.Fatalf("pattern length %d exceeds %d", len(got), DebugQueryPatternMaxBytes)
	}
}

func TestSynthesizeNamesRegressedQuery(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{
			PreviousDeploymentTag: "v80",
			Dependencies: []api.DebugDependencyLatencyItem{{
				Type: AppDependencyType, Kind: "postgresql", Name: "SELECT orders",
				BaselineP95MS: 82, CurrentP95MS: 191, RegressionFactor: 2.33, Regression: true,
				Queries: []api.DebugDependencyQueryItem{
					{Pattern: "SELECT * FROM orders WHERE customer_id = ?", BaselineP95MS: 40, CurrentP95MS: 210, Regression: true},
					{Pattern: "SELECT * FROM orders WHERE id = ?", BaselineP95MS: 5, CurrentP95MS: 5},
				},
			}},
		},
	}
	got := Synthesize(evidence)
	var detail string
	for _, finding := range got.Findings {
		if finding.Code == "dependency_regression" {
			detail = finding.Detail
		}
	}
	if !strings.Contains(detail, "`SELECT * FROM orders WHERE customer_id = ?` (p95 40ms to 210ms)") {
		t.Fatalf("dependency_regression detail = %q, want the regressed query", detail)
	}
	if q := RegressedQuery(evidence.DependencyComparison.Dependencies[0], true); q != nil {
		t.Fatalf("no query failure-regressed, got %+v", q)
	}
}
