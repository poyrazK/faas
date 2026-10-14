package debugger

import (
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// DebugQueryPatternMaxBytes bounds a query pattern; longer statements are
// cut, which still groups their calls consistently.
const DebugQueryPatternMaxBytes = 256

var (
	// sanitizeDebugDBStatement turns "$1" into "$?"; numbered and named
	// placeholders mean the same as a literal. A named placeholder is a
	// colon not preceded by a word character or another colon, so a
	// Postgres cast such as "id::uuid" is left alone.
	queryNumberedPlaceholder = regexp.MustCompile(`\$\?|\$\d+`)
	queryNamedPlaceholder    = regexp.MustCompile(`(^|[^:\w]):[A-Za-z_][A-Za-z0-9_]*`)
	queryInList              = regexp.MustCompile(`(?i)\bIN\s*\(\s*\?(?:\s*,\s*\?)*\s*\)`)
	queryValuesRow           = `\(\s*\?(?:\s*,\s*\?)*\s*\)`
	queryValuesRows          = regexp.MustCompile(`(?i)\bVALUES\s*(` + queryValuesRow + `)(?:\s*,\s*` + queryValuesRow + `)+`)
)

// QueryPattern normalizes a redacted database statement into the pattern
// its calls are grouped by, so `WHERE id = 42` and `WHERE id = 43` count as
// one query. Literals are already "?" in the redacted statement; this also
// folds placeholders, IN lists and multi-row VALUES so batch sizes do not
// split a query, and collapses whitespace. Empty without a statement.
func QueryPattern(statement string) string {
	statement = strings.TrimSpace(sanitizeDebugDBStatement(statement))
	if statement == "" {
		return ""
	}
	statement = queryNumberedPlaceholder.ReplaceAllString(statement, "?")
	statement = queryNamedPlaceholder.ReplaceAllString(statement, "${1}?")
	statement = queryInList.ReplaceAllString(statement, "IN (?)")
	statement = queryValuesRows.ReplaceAllString(statement, "VALUES $1")
	statement = strings.Join(strings.Fields(statement), " ")
	return boundDebugEvidenceText(statement, DebugQueryPatternMaxBytes)
}

// RegressedQuery returns the query pattern within a dependency that
// regressed: on latency, or with failures set, on its error rate. Queries
// arrive regressions-first.
func RegressedQuery(item api.DebugDependencyLatencyItem, failures bool) *api.DebugDependencyQueryItem {
	for i := range item.Queries {
		query := item.Queries[i]
		if (!failures && query.Regression) || (failures && query.FailureRegression) {
			return &query
		}
	}
	return nil
}
