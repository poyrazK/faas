package outbound

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	dependencyTypeKey = "gregale.dependency.type"
	integrationIDKey  = "gregale.outbound.integration_id"
	appIDKey          = "gregale.outbound.app_id"
	originHostKey     = "gregale.outbound.origin_host"
	originSchemeKey   = "gregale.outbound.origin_scheme"
)

func dependencySpanAttributes(integration Integration, appID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(dependencyTypeKey, "outbound_integration"),
		attribute.String(integrationIDKey, integration.ID),
		attribute.String(appIDKey, appID),
	}
	if integration.Origin != nil {
		// Keep destination metadata bounded and credential-free. The request
		// path and query are intentionally not copied into the binding span.
		attrs = append(attrs,
			attribute.String(originHostKey, integration.Origin.Hostname()),
			attribute.String(originSchemeKey, integration.Origin.Scheme),
		)
	}
	return attrs
}

// dependencyTransport creates a child client span for the actual provider
// request. It deliberately records only method, scheme, host, protocol, and
// response status. The integration path and query may contain tenant secrets,
// so they must not be emitted as span attributes.
type dependencyTransport struct {
	base http.RoundTripper
}

func newDependencyTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &dependencyTransport{base: base}
}

func (t *dependencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("outbound: nil request")
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", method),
		attribute.String("url.scheme", requestScheme(req)),
		attribute.String("server.address", requestHost(req)),
	}
	ctx, span := otel.GetTracerProvider().Tracer("gregale/outbound").Start(req.Context(), "HTTP "+method,
		attributeSpanKindClient(),
		oteltrace.WithAttributes(attrs...),
	)
	propagationCarrier := propagation.HeaderCarrier(req.Header)
	otel.GetTextMapPropagator().Inject(ctx, propagationCarrier)
	req = req.Clone(ctx)

	if req.URL != nil {
		ctx = httptrace.WithClientTrace(ctx, clientTrace(span))
		req = req.Clone(ctx)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "upstream request failed")
		span.End()
		return resp, err
	}
	if resp == nil {
		err = io.ErrUnexpectedEOF
		span.RecordError(err)
		span.SetStatus(codes.Error, "upstream returned no response")
		span.End()
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusBadRequest {
		span.SetStatus(codes.Error, strconv.Itoa(resp.StatusCode))
	}
	if resp.Body == nil {
		span.End()
		return resp, nil
	}
	resp.Body = &spanReadCloser{ReadCloser: resp.Body, span: span}
	return resp, nil
}

func attributeSpanKindClient() oteltrace.SpanStartOption {
	return oteltrace.WithSpanKind(oteltrace.SpanKindClient)
}

func requestScheme(req *http.Request) string {
	if req != nil && req.URL != nil && req.URL.Scheme != "" {
		return req.URL.Scheme
	}
	return "unknown"
}

func requestHost(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "unknown"
	}
	return req.URL.Hostname()
}
func clientTrace(span oteltrace.Span) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { span.AddEvent("dns.start") },
		DNSDone:  func(httptrace.DNSDoneInfo) { span.AddEvent("dns.done") },
		ConnectStart: func(_, _ string) {
			span.AddEvent("connect.start")
		},
		ConnectDone: func(_, _ string, err error) {
			if err != nil {
				span.RecordError(err)
			}
			span.AddEvent("connect.done")
		},
		TLSHandshakeStart: func() { span.AddEvent("tls.start") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				span.RecordError(err)
			}
			span.AddEvent("tls.done")
		},
		GotFirstResponseByte: func() { span.AddEvent("response.first_byte") },
	}
}

type spanReadCloser struct {
	io.ReadCloser
	span oteltrace.Span
	once sync.Once
}

func (r *spanReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		if err != io.EOF {
			r.span.RecordError(err)
			r.span.SetStatus(codes.Error, "response body read failed")
		}
		r.end()
	}
	return n, err
}

func (r *spanReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if err != nil {
		r.span.RecordError(err)
		r.span.SetStatus(codes.Error, "response body close failed")
	}
	r.end()
	return err
}

func (r *spanReadCloser) end() {
	r.once.Do(func() { r.span.End() })
}
