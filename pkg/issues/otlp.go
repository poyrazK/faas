package issues

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	logscollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ExtractOTLP accepts JSON-protobuf trace or log exports. The destination is
// an exception exporter, not a general-purpose span or log archive. IDs are
// stable across an exporter retry, independent of receiving time.
func ExtractOTLP(raw []byte, signal string) ([]api.IssueEvent, error) {
	groups, err := ExtractOTLPGroups(raw, signal)
	if err != nil {
		return nil, err
	}
	var events []api.IssueEvent
	for _, group := range groups {
		if group.Err != nil {
			return nil, group.Err
		}
		events = append(events, group.Events...)
	}
	return events, nil
}

// OTLPGroup is one log record or one span containing exception events. Rejecting
// a group as a unit lets OTLP report rejected log/span counts accurately.
type OTLPGroup struct {
	Events []api.IssueEvent
	Err    error
}

func ExtractOTLPGroups(raw []byte, signal string) ([]OTLPGroup, error) {
	raw, err := otlpJSONIDs(raw, signal)
	if err != nil {
		return nil, err
	}
	groups := []OTLPGroup{}
	count := 0
	// Preserve the existing ID for the first occurrence while distinguishing
	// identical records within one export. A stable explicit ID remains the
	// only way to distinguish identical records in independently sent exports.
	seen := make(map[string]int)
	add := func(group *OTLPGroup, attrs []*commonpb.KeyValue, trace, span []byte, nanos uint64) error {
		values := map[string]string{}
		for _, a := range attrs {
			values[a.GetKey()] = a.GetValue().GetStringValue()
		}
		if values["exception.type"] == "" && values["exception.message"] == "" {
			return nil
		}
		count++
		if count > api.IssueMaxBatchEvents {
			return errors.New("too many exception events in one export")
		}
		if nanos == 0 || nanos > uint64(1<<63-1) {
			return errors.New("exception timestamp is required and must fit int64")
		}
		if signal == "logs" {
			// OTLP log correlation is optional: invalid IDs mean that the
			// record is uncorrelated, not that the exception is invalid.
			if len(trace) != 16 || bytes.Equal(trace, make([]byte, 16)) {
				trace = nil
			}
			if len(span) != 8 || bytes.Equal(span, make([]byte, 8)) {
				span = nil
			}
		} else if len(trace) != 16 || bytes.Equal(trace, make([]byte, 16)) || len(span) != 8 || bytes.Equal(span, make([]byte, 8)) {
			return errors.New("trace spans require valid trace and span IDs")
		}
		typ := values["exception.type"]
		if typ == "" {
			typ = "Exception"
		}
		event := api.IssueEvent{OccurredAt: time.Unix(0, int64(nanos)).UTC(), ExceptionType: typ, Message: values["exception.message"], StackTrace: values["exception.stacktrace"], TraceID: hex.EncodeToString(trace), SpanID: hex.EncodeToString(span), RequestID: values["gregale.request_id"], InvocationID: values["gregale.invocation_id"], SourceKind: "exception"}
		event.GroupingStrategy = values["gregale.issue.grouping_strategy"]
		event.SymbolicationStrategy = values["gregale.issue.symbolication_strategy"]
		event.FrameOrder = values["gregale.issue.frame_order"]
		event.ErrorCode = values["gregale.issue.error_code"]
		event.FingerprintOverride = values["gregale.issue.fingerprint_override"]
		for key, value := range values {
			if tag, ok := strings.CutPrefix(key, "gregale.issue.tag."); ok {
				if event.Tags == nil {
					event.Tags = map[string]string{}
				}
				event.Tags[tag] = value
			}
		}
		if raw := values["gregale.issue.breadcrumbs"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &event.Breadcrumbs); err != nil {
				return errors.New("invalid issue breadcrumbs JSON")
			}
		}
		if raw := values["gregale.issue.breadcrumbs_dropped"]; raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return errors.New("invalid dropped breadcrumb count")
			}
			event.BreadcrumbsDropped = value
		}
		identity, _ := json.Marshal(event)
		key := string(identity)
		ordinal := seen[key]
		seen[key]++
		if ordinal > 0 {
			identity = append(identity, []byte("\x00occurrence:"+strconv.Itoa(ordinal))...)
		}
		event.EventID = uuid.NewSHA1(uuid.NameSpaceOID, identity).String()
		if explicit := values["gregale.issue.event_id"]; explicit != "" {
			event.EventID = explicit
		}
		group.Events = append(group.Events, event)
		return nil
	}
	switch signal {
	case "traces":
		var request tracecollector.ExportTraceServiceRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &request); err != nil {
			return nil, errors.New("invalid OTLP trace JSON")
		}
		for _, resource := range request.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					group := OTLPGroup{}
					for _, event := range span.GetEvents() {
						if event.GetName() == "exception" || event.GetName() == "http.server.request.exception" {
							if err := add(&group, event.GetAttributes(), span.GetTraceId(), span.GetSpanId(), event.GetTimeUnixNano()); err != nil {
								if count > api.IssueMaxBatchEvents {
									return nil, err
								}
								group.Err = err
							}
						}
					}
					if len(group.Events) != 0 || group.Err != nil {
						groups = append(groups, group)
					}
				}
			}
		}
	case "logs":
		var request logscollector.ExportLogsServiceRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &request); err != nil {
			return nil, errors.New("invalid OTLP log JSON")
		}
		for _, resource := range request.GetResourceLogs() {
			for _, scope := range resource.GetScopeLogs() {
				for _, record := range scope.GetLogRecords() {
					group := OTLPGroup{}
					nanos := record.GetTimeUnixNano()
					if nanos == 0 {
						nanos = record.GetObservedTimeUnixNano()
					}
					if err := add(&group, record.GetAttributes(), record.GetTraceId(), record.GetSpanId(), nanos); err != nil {
						if count > api.IssueMaxBatchEvents {
							return nil, err
						}
						group.Err = err
					}
					if len(group.Events) != 0 || group.Err != nil {
						groups = append(groups, group)
					}
				}
			}
		}
	default:
		return nil, errors.New("unsupported OTLP signal")
	}
	return groups, nil
}

// OTLP JSON uses hexadecimal IDs rather than protobuf JSON's base64 bytes.
// Only known record paths are transformed; unknown extensions stay ignorable.
func otlpJSONIDs(raw []byte, signal string) ([]byte, error) {
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // OTLP nanosecond timestamps must not pass through float64.
	if !json.Valid(raw) || decoder.Decode(&body) != nil {
		return nil, errors.New("invalid OTLP JSON")
	}
	paths := [][]string{{"resourceSpans", "resource_spans"}, {"scopeSpans", "scope_spans"}, {"spans"}}
	if signal == "logs" {
		paths = [][]string{{"resourceLogs", "resource_logs"}, {"scopeLogs", "scope_logs"}, {"logRecords", "log_records"}}
	}
	var visit func(map[string]any, int) error
	visit = func(node map[string]any, level int) error {
		if level < len(paths) {
			for _, key := range paths[level] {
				array, _ := node[key].([]any)
				for _, child := range array {
					if object, ok := child.(map[string]any); ok {
						if err := visit(object, level+1); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
		for key, size := range map[string]int{"traceId": 16, "spanId": 8, "trace_id": 16, "span_id": 8} {
			rawID, exists := node[key]
			if !exists || rawID == nil {
				continue
			}
			value, ok := rawID.(string)
			if !ok {
				node[key] = "AA==" // invalid length; reject only its log/span later
				continue
			}
			if value == "" {
				continue
			}
			// Accept standard hex plus legacy protobuf JSON for existing callers.
			if len(value) == size*2 {
				bytes, err := hex.DecodeString(value)
				if err != nil {
					node[key] = "AA=="
					continue
				}
				node[key] = base64.StdEncoding.EncodeToString(bytes)
			} else {
				valid := false
				for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
					if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == size {
						valid = true
						break
					}
				}
				if !valid {
					node[key] = "AA=="
				}
			}
		}
		return nil
	}
	if err := visit(body, 0); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}
