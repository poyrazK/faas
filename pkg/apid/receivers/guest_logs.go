// apid-side receiver for in-guest OTLP log exports (ADR-213 runtime adapter,
// ADR-958 bridge).
//
// Direction: vmmd → apid IngestGuestLogs on the SpansWriter service, with the
// same host-owned identity as trace exports. apid owns plan admission, the
// per-account log export cap, OTLP decoding, redaction and bounding, and the
// write into log_events as source=runtime rows. Only records that carry a
// trace ID are kept: the projection exists to answer "what did this request
// log", and untraced lines already reach the runtime ring and archives.

package receivers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/google/uuid"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/state"
)

type guestLogsWriter interface {
	InsertLogEvent(ctx context.Context, event state.LogEvent) (state.LogEvent, error)
}

// guestLogsIngester writes guest log exports. Its limiter is separate from
// the span ingester's so log volume never consumes the trace budget.
type guestLogsIngester struct {
	writer   guestLogsWriter
	limiter  *peraccount.Limiter
	redactor *redact.Redactor
	now      func() time.Time
}

// WithLogs enables guest log ingestion on the span ingester's accounts and
// plan cache, writing through writer (apid's store).
func (g *GuestSpansIngester) WithLogs(writer guestLogsWriter) *GuestSpansIngester {
	g.logs = &guestLogsIngester{
		writer:   writer,
		limiter:  peraccount.NewLimiter(),
		redactor: redact.New(api.TraceMaxLogMessageBytes),
		now:      time.Now,
	}
	return g
}

func (g *GuestSpansIngester) ingestLogs(ctx context.Context, req *apidpb.IngestGuestSpansRequest) *apidpb.IngestGuestLogsResponse {
	outcome := func(value string) *apidpb.IngestGuestLogsResponse {
		return &apidpb.IngestGuestLogsResponse{Outcome: value}
	}
	if g.logs == nil {
		return outcome(guestSpansUnavailable)
	}
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil || req.GetAppId() == "" || req.GetDeploymentId() == "" || req.GetCodec() > 0xff {
		return outcome(guestSpansInvalid)
	}
	contentType, contentEncoding, ok := api.TraceCodecMediaType(byte(req.GetCodec()))
	if !ok || len(req.GetPayload()) == 0 || len(req.GetPayload()) > api.TraceMaxFrameBytes {
		return outcome(guestSpansInvalid)
	}
	limits, err := g.limitsFor(ctx, accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return outcome(guestSpansDisabled)
		}
		g.log.Warn("guest logs: account lookup failed", "err", err)
		return outcome(guestSpansUnavailable)
	}
	if !limits.DebugTelemetryEnabled || limits.DebugGuestLogExportsPerMinute <= 0 {
		return outcome(guestSpansDisabled)
	}
	if taken, retryMs := g.logs.limiter.Take(accountID, limits.DebugGuestLogExportsPerMinute); !taken {
		return &apidpb.IngestGuestLogsResponse{Outcome: guestSpansRateLimited, RetryAfterMs: retryMs}
	}
	export, err := decodeGuestLogsExport(contentType, contentEncoding, req.GetPayload())
	if err != nil {
		return outcome(guestSpansInvalid)
	}
	identity := guestLogIdentity{
		accountID: accountID.String(), appID: req.GetAppId(),
		deploymentID: req.GetDeploymentId(), instanceID: req.GetInstanceId(),
	}
	accepted, rejected := 0, 0
	for _, record := range guestLogRecords(export) {
		if accepted+rejected >= api.TraceMaxLogRecordsPerExport {
			rejected++
			continue
		}
		event, ok := g.logs.project(identity, record)
		if !ok {
			rejected++
			continue
		}
		if _, err := g.logs.writer.InsertLogEvent(ctx, event); err != nil {
			if isGuestLogValidation(err) {
				rejected++
				continue
			}
			// Source event IDs make a retried export idempotent, so a
			// database failure asks the SDK to retry the whole export.
			g.log.Warn("guest logs: insert failed", "err", err)
			return &apidpb.IngestGuestLogsResponse{Outcome: guestSpansUnavailable, AcceptedRecords: int64(accepted), RejectedRecords: int64(rejected)}
		}
		accepted++
	}
	return &apidpb.IngestGuestLogsResponse{Outcome: guestSpansAccepted, AcceptedRecords: int64(accepted), RejectedRecords: int64(rejected)}
}

// IngestGuestLogs implements the SpansWriter RPC.
func (r *spansWriterReceiver) IngestGuestLogs(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestLogsResponse, error) {
	if !r.enabled || r.guest == nil {
		return &apidpb.IngestGuestLogsResponse{Outcome: guestSpansUnavailable}, nil
	}
	refused, err := r.guest.bindToPeerNode(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "guest logs: %v", err)
	}
	if refused != "" {
		return &apidpb.IngestGuestLogsResponse{Outcome: refused}, nil
	}
	return r.guest.ingestLogs(ctx, req), nil
}

func decodeGuestLogsExport(contentType, contentEncoding string, payload []byte) (*collectorlogspb.ExportLogsServiceRequest, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	raw, err := gateway.DecodeOTLPContentEncoding(contentEncoding, payload, api.TraceMaxDecodedBytes)
	if err != nil {
		return nil, err
	}
	var export collectorlogspb.ExportLogsServiceRequest
	switch mediaType {
	case "application/x-protobuf":
		err = proto.Unmarshal(raw, &export)
	case "application/json":
		// The managed bootstraps export protobuf. OTLP/JSON encodes IDs as
		// hex, which protojson would misread, so such exports are refused
		// rather than stored without their trace correlation.
		err = errors.New("OTLP/JSON log exports are not supported")
	default:
		err = fmt.Errorf("unsupported content type %q", mediaType)
	}
	if err != nil {
		return nil, err
	}
	return &export, nil
}

func guestLogRecords(export *collectorlogspb.ExportLogsServiceRequest) []*logspb.LogRecord {
	var out []*logspb.LogRecord
	for _, resource := range export.GetResourceLogs() {
		for _, scope := range resource.GetScopeLogs() {
			out = append(out, scope.GetLogRecords()...)
		}
	}
	return out
}

type guestLogIdentity struct {
	accountID, appID, deploymentID, instanceID string
}

// project turns one OTLP record into a redacted, bounded log_events row.
// Records without a trace ID or message are not kept.
func (l *guestLogsIngester) project(identity guestLogIdentity, record *logspb.LogRecord) (state.LogEvent, bool) {
	traceID := otlpID(record.GetTraceId(), 16)
	message := strings.TrimRight(guestLogBody(record.GetBody()), "\r\n")
	if traceID == "" || strings.TrimSpace(message) == "" {
		return state.LogEvent{}, false
	}
	message, _ = l.redactor.Apply(message)
	occurred := time.Unix(0, int64(record.GetTimeUnixNano()))
	if record.GetTimeUnixNano() == 0 {
		occurred = time.Unix(0, int64(record.GetObservedTimeUnixNano()))
	}
	now := l.now()
	if occurred.Unix() <= 0 || occurred.After(now.Add(time.Minute)) {
		occurred = now
	}
	fields := json.RawMessage(`{}`)
	if spanID := otlpID(record.GetSpanId(), 8); spanID != "" {
		fields = json.RawMessage(`{"span_id":"` + spanID + `"}`)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.instanceID, traceID, otlpID(record.GetSpanId(), 8),
		fmt.Sprint(record.GetTimeUnixNano(), "/", record.GetObservedTimeUnixNano()),
		record.GetSeverityNumber().String(), message,
	}, "\x00")))
	return state.LogEvent{
		OccurredAt:    occurred,
		AccountID:     identity.accountID,
		AppID:         identity.appID,
		DeploymentID:  identity.deploymentID,
		InstanceID:    identity.instanceID,
		Source:        state.LogEventSourceRuntime,
		SourceEventID: "otlp:" + hex.EncodeToString(sum[:16]),
		TraceID:       traceID,
		Level:         guestLogLevel(record.GetSeverityNumber(), record.GetSeverityText()),
		Message:       message,
		Fields:        fields,
	}, true
}

// otlpID hex-encodes a trace or span ID of the expected length; all-zero or
// malformed IDs read as absent.
func otlpID(id []byte, length int) string {
	if len(id) != length {
		return ""
	}
	for _, b := range id {
		if b != 0 {
			return hex.EncodeToString(id)
		}
	}
	return ""
}

// guestLogBody renders a record body as text. Structured bodies are encoded
// as compact JSON; the result is redacted and bounded by the caller.
func guestLogBody(body *commonpb.AnyValue) string {
	if body == nil {
		return ""
	}
	if value, ok := body.GetValue().(*commonpb.AnyValue_StringValue); ok {
		return value.StringValue
	}
	raw, err := protojson.Marshal(body)
	if err != nil {
		return ""
	}
	return string(raw)
}

// guestLogLevel maps OTLP severity to the log_events level vocabulary.
func guestLogLevel(number logspb.SeverityNumber, text string) string {
	switch n := int32(number); {
	case n >= 21:
		return "fatal"
	case n >= 17:
		return "error"
	case n >= 13:
		return "warn"
	case n >= 9:
		return "info"
	case n >= 5:
		return "debug"
	case n >= 1:
		return "trace"
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "fatal", "critical", "crit", "panic", "emergency", "alert":
		return "fatal"
	case "error", "err":
		return "error"
	case "warn", "warning":
		return "warn"
	case "info", "notice":
		return "info"
	case "debug":
		return "debug"
	case "trace":
		return "trace"
	}
	return ""
}

// isGuestLogValidation reports a state-layer refusal (normalization or a
// mapped constraint error, both "state: ..."), as opposed to a database
// failure that the SDK should retry.
func isGuestLogValidation(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "state: ")
}
