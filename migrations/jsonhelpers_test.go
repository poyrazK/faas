//go:build !no_pg

package migrations_test

import (
	"bytes"
	"encoding/json"
	"testing"
)

// compactJSON renders b with no insignificant whitespace.
//
// Postgres prints jsonb with a space after every ':' and ',', so a test that
// substring-matches a Go-marshalled literal (`"max_body_bytes":5242880`)
// never matches the value it just round-tripped. Comparing compacted forms
// keeps those assertions about the DATA and not about the server's
// rendering.
func compactJSON(t *testing.T, b []byte) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatalf("compact jsonb %s: %v", b, err)
	}
	return out.String()
}
