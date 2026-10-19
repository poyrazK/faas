package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/chaos"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type scenarioHTTPResponseFault struct {
	rule                           chaos.Rule
	runID, callerAppID, generation string
}

func (p *ServiceProxy) observeScenarioHTTPFault(span oteltrace.Span, runID, callerAppID, generation string, rule chaos.Rule) {
	span.SetAttributes(attribute.Bool("gregale.chaos.injected", true), attribute.String("gregale.chaos.kind", rule.Kind))
	p.metrics.ObserveServiceChaosInjection(rule.Kind)
	if p.observeChaosMatch != nil {
		p.observeChaosMatch(runID, callerAppID, generation, rule)
	}
}

// Suppress eligible response bytes without storing the body. Errors stream
// through unchanged; the existing forwarder remains responsible for retries,
// request cancellation and draining the upstream response.
type scenarioHTTPResponseWriter struct {
	dst              http.ResponseWriter
	header           http.Header
	status           int
	discard, hasBody bool
}

func newScenarioHTTPResponseWriter(dst http.ResponseWriter) *scenarioHTTPResponseWriter {
	return &scenarioHTTPResponseWriter{dst: dst, header: dst.Header().Clone()}
}
func (w *scenarioHTTPResponseWriter) Header() http.Header         { return w.header }
func (w *scenarioHTTPResponseWriter) Unwrap() http.ResponseWriter { return w.dst }
func (w *scenarioHTTPResponseWriter) copyHeaders() {
	for name, values := range w.header {
		w.dst.Header()[name] = append([]string(nil), values...)
	}
}
func (w *scenarioHTTPResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if isInformationalStatus(status) {
		writeInformationalResponse(w.dst, status, w.header)
		return
	}
	w.status = status
	w.discard = status >= 200 && status < 300 && status != http.StatusNoContent && status != http.StatusResetContent
	if !w.discard {
		w.copyHeaders()
		w.dst.WriteHeader(status)
	}
}
func (w *scenarioHTTPResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.discard {
		w.hasBody = w.hasBody || len(body) > 0
		return len(body), nil
	}
	return w.dst.Write(body)
}
func (w *scenarioHTTPResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.discard {
		if f, ok := w.dst.(http.Flusher); ok {
			f.Flush()
		}
	}
}
func (w *scenarioHTTPResponseWriter) finish(ctx context.Context, injected func()) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.discard {
		w.copyHeaders()
		return
	} // Preserve trailers on pass-through responses.
	if !w.hasBody {
		w.copyHeaders()
		w.dst.WriteHeader(w.status)
		return
	}
	if ctx.Err() != nil {
		return
	} // Cancellation is not evidence of an injected fault.
	injected()
	w.dst.Header().Set("Cache-Control", "no-store")
	w.dst.Header().Set("Content-Length", "1")
	w.dst.WriteHeader(w.status)
	// No response bytes: HTTP/1 closes short; HTTP/2 reports a length mismatch.
}
