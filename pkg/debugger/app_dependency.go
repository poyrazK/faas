package debugger

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// AppDependencyType classifies a customer-emitted OpenTelemetry client span
// (database, HTTP, RPC or messaging call) recognised from standard semantic
// conventions (ADR-958 §5). Platform-owned types keep precedence.
const AppDependencyType = "app_dependency"

const (
	appDependencyMaxNameBytes = 96
	appDependencyMaxKindBytes = 32
)

var (
	sqlLeadingKeyword = regexp.MustCompile(`(?i)^\s*(select|insert|update|delete|upsert|merge|with|call|exec|execute|create|alter|drop|truncate|begin|commit|rollback|set|show|copy)\b`)
	sqlTableAfter     = regexp.MustCompile(`(?i)\b(?:from|into|update|join|table)\s+((?:"[^"]+"|[a-z_][a-z0-9_$]*)(?:\.(?:"[^"]+"|[a-z_][a-z0-9_$]*))?)`)
	identifierSafe    = regexp.MustCompile(`[^A-Za-z0-9 ._:/\-]+`)
)

// classifyAppDependency derives a bounded dependency identity from a span's
// standard attributes. It returns the dependency kind (for example
// "postgresql", "redis", "http", "grpc", "kafka") and a grouping name that
// never contains literals, credentials, paths or query strings:
//
//   - databases: operation and collection/table ("SELECT orders"); Redis and
//     other key-value stores report only the command ("GET")
//   - HTTP clients: the destination host ("api.stripe.com")
//   - RPC: service/method; messaging: operation and destination
func classifyAppDependency(span StoredSpan) (kind, name string, ok bool) {
	attrs := span.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	if system := firstAttr(attrs, "db.system.name", "db.system"); system != "" {
		return normalizeDependencyKind(system), databaseOperationName(attrs, span.DBStatement), true
	}
	if !clientSpanKind(span.Kind) {
		return "", "", false
	}
	if system := firstAttr(attrs, "rpc.system"); system != "" {
		name := strings.Trim(firstAttr(attrs, "rpc.service")+"/"+firstAttr(attrs, "rpc.method"), "/")
		return normalizeDependencyKind(system), boundDependencyName(name, span.Name), true
	}
	if system := firstAttr(attrs, "messaging.system"); system != "" {
		name := strings.TrimSpace(firstAttr(attrs, "messaging.operation.name", "messaging.operation.type", "messaging.operation") + " " +
			firstAttr(attrs, "messaging.destination.name", "messaging.destination"))
		return normalizeDependencyKind(system), boundDependencyName(name, span.Name), true
	}
	if firstAttr(attrs, "http.request.method", "http.method") != "" {
		host := dependencyHost(attrs)
		if host == "" {
			host = "unknown-host"
		}
		return "http", host, true
	}
	return "", "", false
}

func clientSpanKind(kind string) bool {
	upper := strings.ToUpper(kind)
	return strings.Contains(upper, "CLIENT") || strings.Contains(upper, "PRODUCER")
}

func firstAttr(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attrs[key]); value != "" {
			return value
		}
	}
	return ""
}

func databaseOperationName(attrs map[string]string, statement string) string {
	operation := strings.ToUpper(firstAttr(attrs, "db.operation.name", "db.operation"))
	collection := firstAttr(attrs, "db.collection.name", "db.sql.table", "db.mongodb.collection", "db.cassandra.table")
	if statement = strings.TrimSpace(firstNonEmpty(statement, firstAttr(attrs, "db.query.text", "db.statement"))); statement != "" {
		if operation == "" {
			if m := sqlLeadingKeyword.FindStringSubmatch(statement); m != nil {
				operation = strings.ToUpper(m[1])
			} else if fields := strings.Fields(statement); len(fields) > 0 && len(fields[0]) <= 32 {
				// Key-value protocols (Redis, Memcached): the command only,
				// never its key or value arguments.
				operation = strings.ToUpper(fields[0])
			}
		}
		if collection == "" && sqlLeadingKeyword.MatchString(statement) {
			if m := sqlTableAfter.FindStringSubmatch(statement); m != nil {
				collection = strings.ReplaceAll(m[1], `"`, "")
			}
		}
	}
	return boundDependencyName(strings.TrimSpace(operation+" "+collection), "query")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// dependencyHost returns only the destination host: no scheme, userinfo,
// port, path or query.
func dependencyHost(attrs map[string]string) string {
	host := firstAttr(attrs, "server.address", "net.peer.name", "http.host", "net.host.name")
	if host == "" {
		if raw := firstAttr(attrs, "url.full", "http.url"); raw != "" {
			if parsed, err := url.Parse(raw); err == nil {
				host = parsed.Hostname()
			}
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || len(host) > 253 {
		return ""
	}
	for _, r := range host {
		if !lowerAlnumOr(r, ".-:") {
			return ""
		}
	}
	return host
}

func normalizeDependencyKind(system string) string {
	kind := strings.ToLower(strings.TrimSpace(system))
	if kind == "postgres" {
		kind = "postgresql"
	}
	if len(kind) == 0 || len(kind) > appDependencyMaxKindBytes {
		return "other"
	}
	for _, r := range kind {
		if !lowerAlnumOr(r, "_.-") {
			return "other"
		}
	}
	return kind
}

// lowerAlnumOr reports whether r is a lowercase ASCII letter, a digit, or
// one of extra.
func lowerAlnumOr(r rune, extra string) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune(extra, r)
}

func boundDependencyName(name, fallback string) string {
	name = strings.Join(strings.Fields(identifierSafe.ReplaceAllString(name, " ")), " ")
	if name == "" {
		name = strings.Join(strings.Fields(identifierSafe.ReplaceAllString(fallback, " ")), " ")
	}
	if len(name) > appDependencyMaxNameBytes {
		name = name[:appDependencyMaxNameBytes]
	}
	if name == "" {
		return "unnamed"
	}
	return name
}
