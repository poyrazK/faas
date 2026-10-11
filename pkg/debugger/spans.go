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
	debugEvidenceQuotedLiteral  = regexp.MustCompile(`'(?:''|[^'])*'`)
	debugEvidenceNumericLiteral = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
)

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
			Status:         boundDebugEvidenceText(span.Status, api.DebugEvidenceMaxSpanTextBytes),
			DBStatement:    sanitizeDebugDBStatement(span.DBStatement),
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

func sanitizeDebugDBStatement(statement string) string {
	statement = debugEvidenceQuotedLiteral.ReplaceAllString(statement, "?")
	statement = debugEvidenceNumericLiteral.ReplaceAllString(statement, "?")
	statement = strings.Join(strings.Fields(statement), " ")
	return boundDebugEvidenceText(statement, 512)
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
