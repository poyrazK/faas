package gateway

import (
	"sort"
	"strings"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

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
	sp.Attributes = boundAttributes(mergeResourceAttributes(sp.Attributes, sp.resource))
	sp.resource = nil
	return sp
}

// spanResourceKeys are the OTLP resource attributes kept on every span of
// the resource, so a span says which service, version and environment
// produced it (the debugger previously dropped resources entirely). The
// list is fixed and short: these keys take priority within
// spanMaxAttributes, and arbitrary resource attributes (host names,
// process command lines) are not retained.
var spanResourceKeys = map[string]bool{
	"deployment.environment.name": true,
	"service.instance.id":         true,
	"service.name":                true,
	"service.namespace":           true,
	"service.version":             true,
	"telemetry.sdk.language":      true,
}

// spanResourceAttributes returns the allowlisted attributes of an OTLP
// resource, or nil when it has none.
func spanResourceAttributes(kvs []*commonpb.KeyValue) map[string]string {
	var out map[string]string
	for _, kv := range kvs {
		if !spanResourceKeys[kv.GetKey()] {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(spanResourceKeys))
		}
		out[kv.GetKey()] = attrAnyToString(kv.GetValue())
	}
	return out
}

// mergeResourceAttributes adds the resource's allowlisted attributes to a
// span's own; a key the span sets itself keeps the span's value.
func mergeResourceAttributes(attrs, resource map[string]string) map[string]string {
	if len(resource) == 0 {
		return attrs
	}
	if attrs == nil {
		attrs = make(map[string]string, len(resource))
	}
	for key, value := range resource {
		if _, set := attrs[key]; !set {
			attrs[key] = value
		}
	}
	return attrs
}

// boundAttributes keeps at most spanMaxAttributes attributes with bounded
// keys and values. The allowlisted resource keys are kept first, then the
// rest by key order, so the choice is stable across POSTs.
func boundAttributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return attrs
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := spanResourceKeys[keys[i]], spanResourceKeys[keys[j]]; ri != rj {
			return ri
		}
		return keys[i] < keys[j]
	})
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
