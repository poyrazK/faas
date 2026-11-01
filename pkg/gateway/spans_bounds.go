package gateway

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/debugger"
)

// Per-span size bounds applied when a customer or guest span is summarized,
// before it is buffered or written. Spans were stored at full size (any
// attribute, the status message, the DB statement twice), so one account
// could write megabytes per trace into the shared request_telemetry rows.
// With these bounds a span stays under ~6 KiB, and a row under the Scale
// cap of 1,000 spans under ~6 MiB.
const (
	spanMaxNameBytes      = 256
	spanMaxAttributes     = 32
	spanMaxAttrKeyBytes   = 128
	spanMaxAttrValueBytes = 256
	spanMaxStatementBytes = 1024
	spanMaxErrorTypeBytes = 128
)

// statementAttributes are the attribute keys a DB statement is read from.
// The statement is kept once, in DBStatement, scrubbed of literals.
var statementAttributes = []string{"db.statement", "db.query.text"}

// boundSpan applies the per-span bounds and keeps no customer values the
// docs promise are not retained: the status message (often an exception
// message) is dropped and the statement's literals are scrubbed.
func boundSpan(sp summarizedSpan) summarizedSpan {
	sp.Name = truncateUTF8(sp.Name, spanMaxNameBytes)
	sp.StatusMessage = ""
	sp.ErrorType = truncateUTF8(sp.ErrorType, spanMaxErrorTypeBytes)
	sp.DBStatement = truncateUTF8(debugger.ScrubStatementLiterals(sp.DBStatement), spanMaxStatementBytes)
	for _, key := range statementAttributes {
		delete(sp.Attributes, key)
	}
	sp.Attributes = boundAttributes(sp.Attributes)
	return sp
}

// boundAttributes keeps at most spanMaxAttributes attributes (by key order,
// so the choice is stable across POSTs) with bounded keys and values.
func boundAttributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return attrs
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > spanMaxAttributes {
		keys = keys[:spanMaxAttributes]
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[truncateUTF8(key, spanMaxAttrKeyBytes)] = truncateUTF8(attrs[key], spanMaxAttrValueBytes)
	}
	return out
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.ToValidUTF8(s, "")
}
