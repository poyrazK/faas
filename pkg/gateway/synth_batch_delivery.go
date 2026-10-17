package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// Batch delivery (ADR-1321): a trigger with delivery "batch" gets one
// invocation per dispatch batch. The function receives every record and
// reports the ones it could not process in batchItemFailures.

// batchInvocationBody is what a batch-delivery function receives.
type batchInvocationBody struct {
	Records []batchInvocationRecord `json:"records"`
}

// batchInvocationRecord carries a JSON payload as data and any other payload
// as data_base64, so the body stays close to the records' own size.
type batchInvocationRecord struct {
	ItemIdentifier string            `json:"item_identifier"`
	Data           json.RawMessage   `json:"data,omitempty"`
	DataBase64     string            `json:"data_base64,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
}

// batchDeliveryEligible reports whether the request may use one invocation.
// Records with a durable invocation identity each own an invocation, so their
// presence keeps per-record delivery.
func batchDeliveryEligible(req batchDispatchRequest) bool {
	if req.Delivery != api.TriggerDeliveryBatch {
		return false
	}
	for _, rec := range req.Records {
		if rec.InvocationID != "" || rec.InvocationAttempt != 0 || rec.InvocationReplayGeneration != 0 {
			return false
		}
	}
	return true
}

// dispatchBatchAsOneInvocation invokes the function once with every record
// and maps its response back to per-record results.
func (s *SynthServer) dispatchBatchAsOneInvocation(ctx context.Context, req batchDispatchRequest, timeout time.Duration) []batchDispatchResult {
	results := make([]batchDispatchResult, 0, len(req.Records))
	body := batchInvocationBody{Records: make([]batchInvocationRecord, 0, len(req.Records))}
	for _, rec := range req.Records {
		payload, err := base64Decode(rec.PayloadB64)
		if err != nil {
			results = append(results, batchDispatchResult{ItemIdentifier: rec.ItemIdentifier, Status: "dead_letter",
				Error: "payload_b64 invalid", Code: "payload_b64_invalid"})
			continue
		}
		item := batchInvocationRecord{ItemIdentifier: rec.ItemIdentifier, Headers: rec.Headers, Metadata: rec.Metadata}
		if len(payload) > 0 && json.Valid(payload) {
			item.Data = json.RawMessage(payload)
		} else {
			item.DataBase64 = rec.PayloadB64
		}
		body.Records = append(body.Records, item)
	}
	if len(body.Records) == 0 {
		return results
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return append(results, batchResults(body.Records, batchDispatchStatusRetry, "batch_encode_failed", err.Error())...)
	}
	invocationCtx, span := otel.GetTracerProvider().Tracer("gregale/trigger").Start(ctx, "gregale.invocation",
		oteltrace.WithSpanKind(oteltrace.SpanKindConsumer),
		oteltrace.WithAttributes(attribute.String("gregale.trigger.kind", req.Source), attribute.Int("gregale.batch.size", len(body.Records))))
	defer span.End()
	invokeCtx, cancel := context.WithTimeout(invocationCtx, timeout)
	defer cancel()
	inv := state.Invocation{
		ID: req.InvocationID + "-batch-" + uuid.NewString(), AppID: req.AppID, Source: state.InvocationSource(req.Source),
		Method: http.MethodPost, Path: "/_triggers/" + req.Source + "/" + req.TriggerID, Payload: encoded,
		Headers: jsonOrEmpty(map[string]string{"Content-Type": "application/json", "X-Gregale-Batch-Size": strconv.Itoa(len(body.Records))}),
	}
	out, statusCode, err := s.invokeWithStatus(invokeCtx, req.AppID, inv)
	if err != nil {
		code := "invoke_error"
		if errors.Is(invokeCtx.Err(), context.DeadlineExceeded) {
			code = "invoke_timeout"
		}
		s.log.Warn("gateway synth: invoke (batch delivery)", "inv", logsanitize.Field(inv.ID), "records", len(body.Records), "err", err)
		span.SetStatus(codes.Error, code)
		return append(results, batchResults(body.Records, batchDispatchStatusRetry, code, err.Error())...)
	}
	succeeded := (statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices) || string(out.State) == batchDispatchStatusSucceeded
	if !succeeded {
		span.SetStatus(codes.Error, "function_state_"+string(out.State))
		return append(results, batchResults(body.Records, batchDispatchStatusRetry, "function_state_"+string(out.State), "function state="+string(out.State))...)
	}
	failed, err := parseBatchFailures(out.Result)
	if err != nil {
		span.SetStatus(codes.Error, "response_malformed")
		return append(results, batchResults(body.Records, batchDispatchStatusRetry, "response_malformed", "function response malformed: "+err.Error())...)
	}
	span.SetStatus(codes.Ok, "")
	for _, item := range body.Records {
		if containsString(failed, item.ItemIdentifier) {
			results = append(results, batchDispatchResult{ItemIdentifier: item.ItemIdentifier, Status: batchDispatchStatusRetry,
				Error: "reported in batchItemFailures", Code: "function_failed"})
			continue
		}
		results = append(results, batchDispatchResult{ItemIdentifier: item.ItemIdentifier, Status: batchDispatchStatusSucceeded,
			DeploymentID: out.ResolvedDeploymentID})
	}
	return results
}

// invokeWithStatus prefers the status-aware dispatcher, as the per-record
// path does.
func (s *SynthServer) invokeWithStatus(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, int, error) {
	if statusDispatcher, ok := s.dispatcher.(StatusAwareSynthDispatcher); ok {
		return statusDispatcher.InvokeWithStatus(ctx, appID, inv)
	}
	out, err := s.dispatcher.Invoke(ctx, appID, inv)
	return out, 0, err
}

func batchResults(records []batchInvocationRecord, status, code, message string) []batchDispatchResult {
	out := make([]batchDispatchResult, 0, len(records))
	for _, item := range records {
		out = append(out, batchDispatchResult{ItemIdentifier: item.ItemIdentifier, Status: status, Error: message, Code: code})
	}
	return out
}
