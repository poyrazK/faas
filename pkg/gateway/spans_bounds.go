package gateway

import (
	"net/url"
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
	system := sp.Attributes["db.system.name"]
	if system == "" {
		system = sp.Attributes["db.system"]
	}
	sp.DBStatement = truncateUTF8(debugger.ScrubStatementForSystem(sp.DBStatement, system), spanMaxStatementBytes)
	for _, key := range statementAttributes {
		delete(sp.Attributes, key)
	}
	sp.Attributes = boundAttributes(sp.Attributes)
	return sp
}

// retainedSpanAttributes are the only span attributes stored with a request:
// what the debugger reads to classify a dependency (pkg/debugger). Anything
// else an instrumentation records (enduser.id, captured request headers,
// custom attributes) is dropped at ingest rather than kept for the plan's
// retention window unread.
var retainedSpanAttributes = map[string]bool{
	"db.system": true, "db.system.name": true, "db.operation": true, "db.operation.name": true,
	"db.collection.name": true, "db.sql.table": true, "db.mongodb.collection": true, "db.cassandra.table": true,
	"rpc.system": true, "rpc.service": true, "rpc.method": true,
	"messaging.system": true, "messaging.operation": true, "messaging.operation.name": true, "messaging.operation.type": true,
	"messaging.destination": true, "messaging.destination.name": true,
	"http.method": true, "http.request.method": true,
	"server.address": true, "net.peer.name": true, "http.host": true, "net.host.name": true,
	"url.full": true, "http.url": true,
	"error.type": true, "exception.type": true,
	"http.response.status_code": true, "http.status_code": true, "rpc.grpc.status_code": true,
}

// retainedSpanAttributePrefixes are platform-authored attribute families.
var retainedSpanAttributePrefixes = []string{"gregale.dependency.", "gregale.service.", "gregale.outbound.", "gregale.internal."}

func retainSpanAttribute(key string) bool {
	if retainedSpanAttributes[key] {
		return true
	}
	for _, prefix := range retainedSpanAttributePrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// boundAttributes keeps the retained attributes, at most spanMaxAttributes
// (by key order, so the choice is stable across POSTs), with bounded keys and
// values. A URL keeps only its scheme and host: its path and query routinely
// carry identifiers and tokens, and only the host names a dependency.
func boundAttributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return attrs
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		if retainSpanAttribute(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > spanMaxAttributes {
		keys = keys[:spanMaxAttributes]
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		value := attrs[key]
		if key == "url.full" || key == "http.url" {
			value = urlOrigin(value)
		}
		out[truncateUTF8(key, spanMaxAttrKeyBytes)] = truncateUTF8(value, spanMaxAttrValueBytes)
	}
	return out
}

// urlOrigin reduces a URL to scheme://host[:port], or "" when it has no host.
func urlOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
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
