package dependencytrace

import (
	"context"
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

// NewDependencyTransport instruments platform-owned HTTP dependencies. The
// attributes supplied by the caller are copied onto each dependency span and
// must not contain request-specific secrets or unbounded identifiers.
//
// The transport intentionally records only bounded request metadata. URLs,
// paths, queries, headers, and bodies are not span attributes because managed
// provider requests can contain tenant or credential material.
func NewDependencyTransport(base http.RoundTripper, attrs ...attribute.KeyValue) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &dependencyTransport{base: base, attrs: append([]attribute.KeyValue(nil), attrs...)}
}

// StartClientSpan starts a platform-owned dependency span with the same
// tracer and client semantics used by NewDependencyTransport. Callers use
// this for dependencies that cross a non-http.Client boundary, such as the
// node-local service proxy.
func StartClientSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return otel.GetTracerProvider().Tracer("gregale/dependency").Start(ctx, name,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(attrs...),
	)
}

type dependencyTransport struct {
	base  http.RoundTripper
	attrs []attribute.KeyValue
}

func (t *dependencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("dependencytrace: nil dependency request")
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	attrs := append([]attribute.KeyValue(nil), t.attrs...)
	attrs = append(attrs,
		attribute.String("http.request.method", method),
		attribute.String("url.scheme", requestScheme(req)),
		attribute.String("server.address", requestHost(req)),
	)
	ctx, span := otel.GetTracerProvider().Tracer("gregale/dependency").Start(req.Context(), "HTTP "+method,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(attrs...),
	)
	ctx = httptrace.WithClientTrace(ctx, clientTrace(span))
	req = req.Clone(ctx)
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

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
