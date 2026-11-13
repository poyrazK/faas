package debugger

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

var (
	debugEvidenceQuotedLiteral = regexp.MustCompile(`'(?:''|\\.|[^'\\])*'`)
	debugEvidenceJSONValue     = regexp.MustCompile(`(:\s*)"(?:\\.|[^"\\])*"`)
	// A number not preceded by `$`, so Postgres placeholders ($1) stay.
	debugEvidenceNumericLiteral = regexp.MustCompile(`(^|[^$\w.])\d+(?:\.\d+)?\b`)
	debugEvidenceHexLiteral     = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`)
	// Double-quoted text is an identifier in standard SQL but a string in
	// MySQL's default mode. It is scrubbed where only a value can stand:
	// after a comparison, and inside IN (...) and VALUES (...) lists.
	debugEvidenceDoubleQuoted        = regexp.MustCompile(`"(?:""|\\.|[^"\\])*"`)
	debugEvidenceComparedDoubleQuote = regexp.MustCompile(`(?i)((?:=|<>|!=|<=|>=|<|>|\bLIKE|\bILIKE)\s*)"(?:""|\\.|[^"\\])*"`)
	debugEvidenceValueList           = regexp.MustCompile(`(?i)\b(?:IN|VALUES)\s*\([^()]*\)(?:\s*,\s*\([^()]*\))*`)
	// In a JSON document (a MongoDB command) every string that is not a key
	// is a value, including those inside arrays.
	debugEvidenceJSONString = regexp.MustCompile(`"(?:\\.|[^"\\])*"(\s*:)?`)
)

// commandStatementSystems record a statement as a command and its arguments
// (`SET session:42 <token>`), with no quoting to tell keys and values apart.
// Only the command is kept.
var commandStatementSystems = map[string]bool{"redis": true, "valkey": true, "keydb": true, "dragonfly": true, "memcached": true}

type StoredSpan struct {
	TraceID           string            `json:"trace_id"`
	SpanID            string            `json:"span_id"`
	ParentSpanID      string            `json:"parent_span_id"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	StartTimeUnixNano uint64            `json:"start_time_unix_nano"`
	EndTimeUnixNano   uint64            `json:"end_time_unix_nano"`
	DurationNanos     uint64            `json:"duration_nanos"`
	Status            string            `json:"status"`
	DBStatement       string            `json:"db_statement"`
	ErrorType         string            `json:"error_type"`
	Attributes        map[string]string `json:"attributes"`
}

// ParseSpans parses the writer's JSON summary, drops sensitive
// fields, sorts slowest first, and caps the response size. Malformed summaries
// are treated as absent evidence so a bad future payload cannot break lookup.
func ParseSpans(raw []byte) ([]api.DebugTelemetrySpan, bool) {
	if len(raw) == 0 {
		return []api.DebugTelemetrySpan{}, false
	}
	var input []StoredSpan
	if err := json.Unmarshal(raw, &input); err != nil {
		return []api.DebugTelemetrySpan{}, false
	}
	sort.SliceStable(input, func(i, j int) bool {
		if input[i].DurationNanos != input[j].DurationNanos {
			return input[i].DurationNanos > input[j].DurationNanos
		}
		return input[i].SpanID < input[j].SpanID
	})
	truncated := len(input) > api.DebugEvidenceMaxSpans
	if truncated {
		input = input[:api.DebugEvidenceMaxSpans]
	}
	out := make([]api.DebugTelemetrySpan, 0, len(input))
	for _, span := range input {
		dependencyType := sanitizeDebugDependencyType(span.Attributes["gregale.dependency.type"])
		dependencyKind := ""
		dependencyName := ""
		if dependencyType != "" {
			dependencyKind = sanitizeDebugDependencyKind(span.Attributes["gregale.dependency.kind"])
		} else if kind, name, ok := classifyAppDependency(span); ok {
			dependencyType, dependencyKind, dependencyName = AppDependencyType, kind, name
		}
		out = append(out, api.DebugTelemetrySpan{
			TraceID:        boundDebugEvidenceText(span.TraceID, api.DebugEvidenceMaxSpanTextBytes),
			SpanID:         boundDebugEvidenceText(span.SpanID, api.DebugEvidenceMaxSpanTextBytes),
			ParentSpanID:   boundDebugEvidenceText(span.ParentSpanID, api.DebugEvidenceMaxSpanTextBytes),
			Name:           boundDebugEvidenceText(span.Name, api.DebugEvidenceMaxSpanTextBytes),
			Kind:           boundDebugEvidenceText(span.Kind, api.DebugEvidenceMaxSpanTextBytes),
			StartTime:      debugEvidenceTime(span.StartTimeUnixNano),
			EndTime:        debugEvidenceTime(span.EndTimeUnixNano),
			DurationNanos:  span.DurationNanos,
			Status:         NormalizeSpanStatus(span.Status),
			DBStatement:    sanitizeDebugDBStatement(span.DBStatement),
			ErrorType:      sanitizeDebugErrorType(span.Status, span.ErrorType),
			DependencyType: dependencyType,
			DependencyKind: dependencyKind,
			DependencyName: dependencyName,
		})
	}
	return out, truncated
}

// SegmentName is the identity a span is grouped under in dependency and
// critical-path rollups: the normalized dependency name when classified,
// otherwise the span name.
func SegmentName(span api.DebugTelemetrySpan) string {
	if span.DependencyName != "" {
		return span.DependencyName
	}
	if span.Name != "" {
		return span.Name
	}
	return "<unnamed>"
}

func debugEvidenceTime(unixNano uint64) string {
	if unixNano == 0 || unixNano > uint64(^uint64(0)>>1) {
		return ""
	}
	return time.Unix(0, int64(unixNano)).UTC().Format(time.RFC3339Nano)
}

func sanitizeDebugDependencyType(value string) string {
	switch strings.TrimSpace(value) {
	case "managed_binding", "outbound_integration", "guest_transport", "platform_internal":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sanitizeDebugDependencyKind(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return ""
		}
	}
	return value
}

func boundDebugEvidenceText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = safetext.Truncate(value, maxBytes)
	return value
}

// NormalizeSpanStatus maps retained span status to "ok", "error" or "".
// Rows written before ingest normalized OTLP codes carry the raw enum names
// ("STATUS_CODE_ERROR"); anything else unrecognised is dropped rather than
// echoed.
func NormalizeSpanStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ERROR", "STATUS_CODE_ERROR":
		return "error"
	case "OK", "STATUS_CODE_OK":
		return "ok"
	default:
		return ""
	}
}

// debugErrorTypeMaxBytes bounds a span failure class such as
// "QueryTimeout", "java.net.SocketTimeoutException" or "503".
const debugErrorTypeMaxBytes = 96

// sanitizeDebugErrorType keeps an error span's failure class only when it
// looks like a type name or code. Free text (an exception message mistakenly
// sent as error.type) is dropped instead of being trimmed into evidence.
func sanitizeDebugErrorType(status, errorType string) string {
	errorType = strings.TrimSpace(errorType)
	if NormalizeSpanStatus(status) != "error" || errorType == "" || len(errorType) > debugErrorTypeMaxBytes {
		return ""
	}
	for i := 0; i < len(errorType); i++ {
		c := errorType[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("._:$/-", rune(c)) {
			return ""
		}
	}
	return errorType
}

func sanitizeDebugDBStatement(statement string) string {
	statement = ScrubStatementLiterals(statement)
	statement = strings.Join(strings.Fields(statement), " ")
	return boundDebugEvidenceText(statement, 512)
}

// ScrubStatementLiterals replaces the literal values in a database statement
// with "?": single-quoted strings (doubled ” and backslash escapes), JSON
// string values (Mongo-style documents, `"key": "value"`) and numbers.
// Double-quoted SQL identifiers stay, so table names survive. Span ingest
// applies it so stored spans keep no customer values (docs/tracing.md); the
// read path applies it again for rows stored before.
//
// Also scrubbed: PostgreSQL dollar-quoted strings ($$…$$, $tag$…$tag$), hex
// literals (0x…), double-quoted strings where only a value can stand (MySQL
// strings after a comparison or in IN/VALUES lists), and, in a JSON document,
// every string that is not a key, array members included.
func ScrubStatementLiterals(statement string) string {
	statement = scrubDollarQuoted(statement)
	statement = debugEvidenceQuotedLiteral.ReplaceAllString(statement, "?")
	if trimmed := strings.TrimSpace(statement); strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.Contains(trimmed, `{"`) {
		statement = debugEvidenceJSONString.ReplaceAllStringFunc(statement, func(match string) string {
			if strings.HasSuffix(match, ":") {
				return match // a key
			}
			return "?"
		})
	} else {
		statement = debugEvidenceJSONValue.ReplaceAllString(statement, "$1?")
		statement = debugEvidenceComparedDoubleQuote.ReplaceAllString(statement, "$1?")
		statement = debugEvidenceValueList.ReplaceAllStringFunc(statement, func(list string) string {
			return debugEvidenceDoubleQuoted.ReplaceAllString(list, "?")
		})
	}
	statement = debugEvidenceHexLiteral.ReplaceAllString(statement, "?")
	return debugEvidenceNumericLiteral.ReplaceAllString(statement, "${1}?")
}

// ScrubStatementForSystem scrubs a statement recorded by a db.system. Command
// stores (Redis and the like) keep only the command; every argument, keys
// included, becomes "?". Other systems get ScrubStatementLiterals.
func ScrubStatementForSystem(statement, system string) string {
	if !commandStatementSystems[strings.ToLower(strings.TrimSpace(system))] {
		return ScrubStatementLiterals(statement)
	}
	fields := strings.Fields(statement)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0]) + strings.Repeat(" ?", len(fields)-1)
}

// scrubDollarQuoted replaces PostgreSQL dollar-quoted strings with "?". A
// positional parameter ($1) is not a quote: a tag starts with a letter or
// underscore, or is empty ($$).
func scrubDollarQuoted(statement string) string {
	var out strings.Builder
	for i := 0; i < len(statement); {
		if statement[i] != '$' {
			out.WriteByte(statement[i])
			i++
			continue
		}
		end := i + 1
		for end < len(statement) && (statement[end] == '_' || statement[end] >= 'a' && statement[end] <= 'z' ||
			statement[end] >= 'A' && statement[end] <= 'Z' || end > i+1 && statement[end] >= '0' && statement[end] <= '9') {
			end++
		}
		if end >= len(statement) || statement[end] != '$' {
			out.WriteByte('$')
			i++
			continue
		}
		tag := statement[i : end+1]
		closing := strings.Index(statement[end+1:], tag)
		if closing < 0 {
			out.WriteByte('$')
			i++
			continue
		}
		out.WriteByte('?')
		i = end + 1 + closing + len(tag)
	}
	return out.String()
}

func minDebugDependencyDuration(duration uint64) uint64 {
	const maxDuration = uint64(24 * time.Hour)
	if duration > maxDuration {
		return maxDuration
	}
	return duration
}

type debugDependencyTimedSpan struct {
	start int64
	end   int64
}

type debugDependencyInterval struct {
	start int64
	end   int64
}

// SpanExclusiveDurations computes wall time not covered by
// overlapping direct children for every retained span. Invalid timestamps
// fall back to the writer-provided duration, keeping the aggregate useful for
// legacy evidence while the response remains explicitly sample-bounded.
func SpanExclusiveDurations(spans []api.DebugTelemetrySpan) []uint64 {
	exclusive := make([]uint64, len(spans))
	timed := make([]debugDependencyTimedSpan, len(spans))
	valid := make([]bool, len(spans))
	byID := make(map[string]int, len(spans))
	for index, span := range spans {
		exclusive[index] = minDebugDependencyDuration(span.DurationNanos)
		start, startErr := time.Parse(time.RFC3339Nano, span.StartTime)
		end, endErr := time.Parse(time.RFC3339Nano, span.EndTime)
		if startErr != nil || endErr != nil || !end.After(start) {
			continue
		}
		timed[index] = debugDependencyTimedSpan{start: start.UnixNano(), end: end.UnixNano()}
		valid[index] = true
		if span.SpanID != "" {
			byID[span.SpanID] = index
		}
	}

	children := make(map[int][]int, len(spans))
	for index, span := range spans {
		parentIndex, ok := byID[span.ParentSpanID]
		if !ok || parentIndex == index || !valid[index] {
			continue
		}
		children[parentIndex] = append(children[parentIndex], index)
	}

	for index, parent := range timed {
		if !valid[index] {
			continue
		}
		intervals := make([]debugDependencyInterval, 0, len(children[index]))
		for _, childIndex := range children[index] {
			if !valid[childIndex] {
				continue
			}
			child := timed[childIndex]
			start, end := child.start, child.end
			if start < parent.start {
				start = parent.start
			}
			if end > parent.end {
				end = parent.end
			}
			if start < end {
				intervals = append(intervals, debugDependencyInterval{start: start, end: end})
			}
		}
		if len(intervals) == 0 {
			exclusive[index] = minDebugDependencyDuration(uint64(parent.end - parent.start))
			continue
		}
		sort.SliceStable(intervals, func(i, j int) bool {
			if intervals[i].start != intervals[j].start {
				return intervals[i].start < intervals[j].start
			}
			return intervals[i].end < intervals[j].end
		})
		covered := int64(0)
		coveredStart, coveredEnd := intervals[0].start, intervals[0].end
		for _, interval := range intervals[1:] {
			if interval.start > coveredEnd {
				covered += coveredEnd - coveredStart
				coveredStart, coveredEnd = interval.start, interval.end
				continue
			}
			if interval.end > coveredEnd {
				coveredEnd = interval.end
			}
		}
		covered += coveredEnd - coveredStart
		value := parent.end - parent.start - covered
		if value < 0 {
			value = 0
		}
		exclusive[index] = minDebugDependencyDuration(uint64(value))
	}
	return exclusive
}
