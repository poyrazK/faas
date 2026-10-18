package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var telemetryRequestID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var telemetryTraceparent = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

func protocolMethodLabel(method string) string {
	switch method {
	case "initialize", "server/discover", "ping", "tools/list", "tools/call", "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "completion/complete", "tasks/get", "tasks/cancel", "tasks/update", "subscriptions/listen":
		return method
	default:
		return "other"
	}
}
func protocolToolLabel(method string, params json.RawMessage) string {
	if method != "tools/call" {
		return "none"
	}
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params, &p)
	switch p.Name {
	case "greet", "add", "stream_demo", "build_report":
		return p.Name
	default:
		return "other"
	}
}

type protocolObservation struct {
	http.ResponseWriter
	request                                   *http.Request
	method, tool, requestID, traceID, outcome string
	start, executionEnd, streamStart          time.Time
	encodingFailed                            bool
	writeFailed                               bool
	status                                    int
}

func observeProtocolRequest(w http.ResponseWriter, r *http.Request, request rpcRequest) (http.ResponseWriter, *protocolObservation) {
	o := &protocolObservation{ResponseWriter: w, request: r, method: protocolMethodLabel(request.Method), tool: protocolToolLabel(request.Method, request.Params), start: time.Now()}
	ids := r.Header.Values("X-Faas-Request-Id")
	if len(ids) == 1 && telemetryRequestID.MatchString(ids[0]) {
		o.requestID = ids[0]
	}
	traces := r.Header.Values("traceparent")
	if len(traces) == 1 {
		m := telemetryTraceparent.FindStringSubmatch(traces[0])
		if len(m) == 3 && m[1] != strings.Repeat("0", 32) && m[2] != strings.Repeat("0", 16) {
			o.traceID = m[1]
		}
	}
	if f, ok := w.(http.Flusher); ok {
		return &protocolFlushingWriter{protocolObservation: o, flusher: f}, o
	}
	return o, o
}
func (o *protocolObservation) Unwrap() http.ResponseWriter { return o.ResponseWriter }
func (o *protocolObservation) WriteHeader(status int) {
	if o.status == 0 {
		o.status = status
		if strings.HasPrefix(o.Header().Get("Content-Type"), "text/event-stream") {
			o.streamStart = time.Now()
		}
	}
	o.ResponseWriter.WriteHeader(status)
}
func (o *protocolObservation) Write(data []byte) (int, error) {
	if o.status == 0 {
		o.WriteHeader(200)
	}
	n, err := o.ResponseWriter.Write(data)
	if err != nil {
		o.writeFailed = true
	}
	return n, err
}

type protocolFlushingWriter struct {
	*protocolObservation
	flusher http.Flusher
}

func (w *protocolFlushingWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	w.flusher.Flush()
}
func (o *protocolObservation) observeRPC(response rpcResponse) {
	if o.outcome != "" {
		return
	}
	if o.executionEnd.IsZero() {
		o.executionEnd = time.Now()
	}
	o.outcome = "success"
	if response.Error != nil {
		o.outcome = "rpc_error"
	} else if result, ok := response.Result.(map[string]any); ok {
		if result["isError"] == true {
			o.outcome = "tool_error"
		} else if result["resultType"] == "task" {
			o.outcome = "task_created"
		}
	}
}
func (o *protocolObservation) acknowledged() {
	if o.executionEnd.IsZero() {
		o.executionEnd = time.Now()
	}
}

func (o *protocolObservation) encodingFailure() {
	if !o.writeFailed {
		o.encodingFailed = true
	}
}
func noteProtocolEncodingFailure(w any) {
	if observer, ok := w.(interface{ encodingFailure() }); ok {
		observer.encodingFailure()
	}
}

func noteProtocolResponse(w any, value any) {
	if value, ok := value.(map[string]any); ok && value["method"] == "notifications/subscriptions/acknowledged" {
		if observer, ok := w.(interface{ acknowledged() }); ok {
			observer.acknowledged()
		}
		return
	}
	if observer, ok := w.(interface{ observeRPC(rpcResponse) }); ok {
		if response, ok := value.(rpcResponse); ok {
			observer.observeRPC(response)
		}
	}
}
func (o *protocolObservation) finish() {
	now := time.Now()
	streamOutcome := "complete"
	if errors.Is(o.request.Context().Err(), context.DeadlineExceeded) {
		streamOutcome = "timeout"
	} else if o.writeFailed || o.request.Context().Err() != nil {
		streamOutcome = "cancelled"
	} else if o.encodingFailed {
		streamOutcome = "internal_error"
	}
	if o.outcome == "" {
		switch {
		case streamOutcome != "complete":
			o.outcome = streamOutcome
		case o.status == 401 || o.status == 403:
			o.outcome = "authorization_error"
		default:
			o.outcome = "unobserved"
		}
	}
	event := map[string]any{"event": "mcp_protocol_request", "method": o.method, "tool": o.tool, "outcome": o.outcome, "stream_outcome": streamOutcome}
	if !o.executionEnd.IsZero() {
		event["execution_duration_ms"] = float64(o.executionEnd.Sub(o.start)) / float64(time.Millisecond)
	}
	if !o.streamStart.IsZero() {
		event["stream_duration_ms"] = float64(now.Sub(o.streamStart)) / float64(time.Millisecond)
	}
	if o.requestID != "" {
		event["request_id"] = o.requestID
	}
	if o.traceID != "" {
		event["trace_id"] = o.traceID
	}
	data, err := json.Marshal(event)
	if err == nil {
		log.Print(string(data))
	}
}

// startProtocolHeartbeat reports liveness independently of RPC traffic.
func startProtocolHeartbeat(ctx context.Context) {
	emit := func() {
		data, _ := json.Marshal(map[string]any{"event": "mcp_telemetry_heartbeat", "version": 1, "loss_alert": os.Getenv("MCP_TELEMETRY_LOSS_ALERT") == "true"})
		_, _ = fmt.Fprintln(os.Stdout, string(data))
	}
	runProtocolHeartbeat(ctx, emit)
}

func runProtocolHeartbeat(ctx context.Context, emit func()) {
	emit()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}
