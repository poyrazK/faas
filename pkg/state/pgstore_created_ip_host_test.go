// Regression pin for the inet + nullable-text scan bugs (review
// finding #1 + CI follow-up, PR #653 mega-PR). The pgstore.go
// SELECT and RETURNING clauses for api_keys scan two newly-added
// nullable columns into Go values via pgx:
//
//   - created_ip  (inet column → Go *string via IP normalization)
//     pgx has no default codec for `inet → *string`, so every
//     SELECT/RETURNING site had to wrap the column with
//     `coalesce(host(created_ip), ”)` to render it as `text` on
//     the wire. (The existing sessions.issued_ip inet column
//     solves the same problem with the same wrap pattern.)
//
//   - created_ua  (text column → Go string)
//     pgx can scan `text → *string` natively, but the APIKey
//     struct's CreatedUA field is a non-pointer `string` (it's
//     always present on every read, just possibly empty). pgx
//     refuses to scan SQL NULL into a non-pointer Go type, so
//     every SELECT/RETURNING site must wrap `created_ua` with
//     `coalesce(created_ua, ”) as created_ua`. The matching
//     struct field for sessions.issued_ua uses the same wrap
//     (see `coalesce(issued_ua, ”) as issued_ua` at
//     pgstore.go:10432).
//
// We pin BOTH wraps via the SQL fragments in pgstore.go's
// source. A live Postgres round-trip would also detect a
// regression but only when a NULL row exists; a contributor
// could "fix" the test by relaxing the SQL query and the
// regression would re-emerge on the next NULL insert.
// Pinning the source closes that gap.
package state_test

import (
	"os"
	"strings"
	"testing"
)

const sourceFile = "pgstore.go"
const hostWrap = "coalesce(host(created_ip),'')"
const uaWrap = "coalesce(created_ua,'')"

// TestPgStore_CreatedProvenanceColumnCoalesce pins that every
// SELECT or RETURNING clause in pgstore.go that touches either
// of the api_keys provenance columns carries the matching
// coalesce wrap:
//
//   - created_ip (inet)      → host() wrap so pgx can scan into *string
//   - created_ua (text NULL) → coalesce(”) so pgx can scan into string
//
// INSERT/UPDATE column-declaration lines + VALUES placeholder
// positions don't need the wrap (pgx encodes Go string → inet /
// text on the write path; the inet → string / NULL → string
// scan only happens on the read path).
func TestPgStore_CreatedProvenanceColumnCoalesce(t *testing.T) {
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("read %s: %v", sourceFile, err)
	}

	// Fail-fast signal: at least 18 SELECT/RETURNING sites must
	// carry the inet wrap. PR #653 landed 20 sites; the CI
	// follow-up added 5 more so the count is ≥ 25 today. A
	// future contributor adding a new query without the wrap
	// will trip this lower bound if they also remove an
	// existing one (the wrap count goes down); a contributor
	// who keeps all existing wraps and adds the missing wrap to
	// new queries keeps the count ≥ 18.
	ipWrapCount := strings.Count(string(data), hostWrap)
	const minIPWrapSites = 18
	if ipWrapCount < minIPWrapSites {
		t.Errorf("%s: only %d `%s` wraps found; want ≥ %d. Every SELECT/RETURNING clause touching `created_ip` must carry the wrap so pgx can scan the result into `*string` (column type is `inet`, no default codec). INSERT/UPDATE column declarations + VALUES placeholders are exempt.",
			sourceFile, ipWrapCount, hostWrap, minIPWrapSites)
	}

	// Same shape for the created_ua wrap (every SELECT/RETURNING
	// site that lands NULL into a non-pointer Go string must
	// carry it). The CI follow-up added the wrap to all 22
	// SELECT/RETURNING sites that share the column list with
	// created_ip; the threshold is 18 to leave headroom.
	uaWrapCount := strings.Count(string(data), uaWrap)
	const minUAWrapSites = 18
	if uaWrapCount < minUAWrapSites {
		t.Errorf("%s: only %d `%s` wraps found; want ≥ %d. Every SELECT/RETURNING clause touching `created_ua` must carry the wrap so pgx can scan the result into `string` (column is nullable `text`; pgx refuses to scan SQL NULL into a non-pointer Go type). INSERT/UPDATE column declarations + VALUES placeholders are exempt.",
			sourceFile, uaWrapCount, uaWrap, minUAWrapSites)
	}

	// Tight pin: every line that mentions `created_ip,` and is
	// NOT already wrapped must be an INSERT column-declaration.
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "created_ip,") {
			continue
		}
		if strings.Contains(line, hostWrap) {
			continue
		}
		trim := strings.TrimSpace(line)
		// INSERT column declarations are bare-string literals that
		// begin a SQL fragment: `insert into api_keys (...)`.
		// The trim may start with ` (backtick + start-of-literal)
		// or just with the `insert` keyword, depending on how the
		// Go formatter folded the raw string. Both shapes exempt.
		if strings.HasPrefix(trim, "insert into api_keys (") ||
			strings.HasPrefix(trim, "`insert into api_keys (") {
			continue // exempt: INSERT column declaration
		}
		t.Errorf("%s:%d: unwrapped `created_ip` reference on what looks like a SELECT/RETURNING clause: %q\n"+
			"    Wrap with `%s as created_ip` so pgx scans into `*string`. "+
			"(Pre-existing sessions.issued_ip column uses the same pattern.)",
			sourceFile, i+1, trim, hostWrap)
	}
}
