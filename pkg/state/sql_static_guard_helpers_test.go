// Package-level helpers shared by the SQL-static TZ guards
// (pgstore_usage_by_hour_tz_sql_test.go and
// pgstore_state_usage_monthly_tz_sql_test.go). Whitebox test
// file (package state, not state_test) so the helpers can read
// //go:embed'd sources directly without exporting them. Kept in a
// dedicated file so future SQL-static guards (e.g. for §10
// invoice/usage surfaces) reuse the same primitives instead of
// duplicating them.

package state_test

import "strings"

// extractFn returns the substring of `src` covering a function
// body, bounded by the next top-level `func (` signature so we
// don't drag in unrelated SQL. The 8 KiB cap is far more than any
// actual function body (~40 lines for the worst case in
// pgstore.go); it exists only to bound the search if the next
// `func (` is missing (malformed source). Returns "" when `sig`
// is not found.
func extractFn(src, sig string) string {
	start := strings.Index(src, sig)
	if start < 0 {
		return ""
	}
	rest := src[start+len(sig):]
	end := strings.Index(rest, "\nfunc (")
	if end < 0 {
		end = len(rest)
	}
	if end > 8192 {
		end = 8192
	}
	return src[start : start+len(sig)+end]
}
