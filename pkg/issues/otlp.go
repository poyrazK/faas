package issues

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	raw, err := otlpJSONIDs(raw, signal)
	if err != nil {
		return nil, err
	}
	events := []api.IssueEvent{}
	add := func(attrs []*commonpb.KeyValue, trace, span []byte, nanos uint64) error {
		values := map[string]string{}
		for _, a := range attrs {
			values[a.GetKey()] = a.GetValue().GetStringValue()
		}
		if values["exception.type"] == "" && values["exception.message"] == "" {
			return nil
		}
		if nanos == 0 || nanos > uint64(1<<63-1) {
			return errors.New("exception timestamp is required and must fit int64")
		}
		typ := values["exception.type"]
		if typ == "" {
			typ = "Exception"
		}
		event := api.IssueEvent{OccurredAt: time.Unix(0, int64(nanos)).UTC(), ExceptionType: typ, Message: values["exception.message"], StackTrace: values["exception.stacktrace"], TraceID: hex.EncodeToString(trace), SpanID: hex.EncodeToString(span), RequestID: values["gregale.request_id"], InvocationID: values["gregale.invocation_id"], SourceKind: "exception"}
		identity, _ := json.Marshal(event)
		event.EventID = uuid.NewSHA1(uuid.NameSpaceOID, identity).String()
		if explicit := values["gregale.issue.event_id"]; explicit != "" {
			event.EventID = explicit
		}
		events = append(events, event)
		if len(events) > api.IssueMaxBatchEvents {
			return errors.New("too many exception events in one export")
		}
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
					for _, event := range span.GetEvents() {
						if event.GetName() == "exception" || event.GetName() == "http.server.request.exception" {
							if err := add(event.GetAttributes(), span.GetTraceId(), span.GetSpanId(), event.GetTimeUnixNano()); err != nil {
								return nil, err
							}
						}
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
					nanos := record.GetTimeUnixNano()
					if nanos == 0 {
						nanos = record.GetObservedTimeUnixNano()
					}
					if err := add(record.GetAttributes(), record.GetTraceId(), record.GetSpanId(), nanos); err != nil {
						return nil, err
					}
				}
			}
		}
	default:
		return nil, errors.New("unsupported OTLP signal")
	}
	return events, nil
}

// OTLP JSON uses hexadecimal IDs rather than protobuf JSON's base64 bytes.
// Only known record paths are transformed; unknown extensions stay ignorable.
func otlpJSONIDs(raw []byte, signal string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("invalid OTLP JSON")
	}
	paths := []string{"resourceSpans", "scopeSpans", "spans"}
	if signal == "logs" {
		paths = []string{"resourceLogs", "scopeLogs", "logRecords"}
	}
	var visit func(map[string]any, int) error
	visit = func(node map[string]any, level int) error {
		if level < len(paths) {
			array, _ := node[paths[level]].([]any)
			for _, child := range array {
				if object, ok := child.(map[string]any); ok {
					if err := visit(object, level+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for key, size := range map[string]int{"traceId": 16, "spanId": 8} {
			value, ok := node[key].(string)
			if !ok || value == "" {
				continue
			}
			// Accept standard hex plus legacy protobuf JSON for existing callers.
			if len(value) == size*2 {
				bytes, err := hex.DecodeString(value)
				if err != nil {
					return errors.New("invalid OTLP hex ID")
				}
				node[key] = base64.StdEncoding.EncodeToString(bytes)
			}
		}
		return nil
	}
	if err := visit(body, 0); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}
