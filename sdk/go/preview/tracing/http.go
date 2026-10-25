package tracing

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Handler records a server span for each request, continuing the trace in
// the inbound traceparent header (the Gregale gateway sends one), and passes
// the span to next in the request context. Wrap the router itself so the span
// is named after the matched ServeMux pattern.
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := active.Load()
		if t == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx, span := t.start(r.Context(), r.Method, KindServer, parseTraceparent(r.Header.Get("traceparent")))
		span.SetAttribute("http.request.method", r.Method)
		span.SetAttribute("url.path", r.URL.Path)
		rec := &statusRecorder{ResponseWriter: w}
		inner := r.WithContext(ctx)
		completed := false
		defer func() {
			// ServeMux records the matched pattern on the request it served.
			if route := routeOf(inner.Pattern); route != "" {
				span.setName(r.Method + " " + route)
				span.SetAttribute("http.route", route)
			}
			status := rec.statusCode()
			if !completed {
				status = http.StatusInternalServerError
			}
			span.setInt("http.response.status_code", int64(status))
			if status >= 500 {
				span.markError(http.StatusText(status))
			}
			span.End()
		}()
		next.ServeHTTP(rec, inner)
		completed = true
	})
}

// routeOf strips the method and host from a ServeMux pattern
// ("GET example.com/orders/{id}" -> "/orders/{id}").
func routeOf(pattern string) string {
	if i := strings.IndexAny(pattern, " \t"); i >= 0 {
		pattern = strings.TrimSpace(pattern[i:])
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 {
		pattern = pattern[i:]
	}
	return pattern
}

// statusRecorder captures the response status. Unwrap lets
// http.ResponseController reach the original writer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("tracing: response writer does not support hijacking")
}

// Transport records a client span for each request and sends the trace on in
// a traceparent header. A nil base uses http.DefaultTransport.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct{ base http.RoundTripper }

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr := active.Load()
	if tr == nil {
		return t.base.RoundTrip(req)
	}
	_, span := tr.start(req.Context(), req.Method, KindClient, spanContext{})
	span.SetAttribute("http.request.method", req.Method)
	span.SetAttribute("url.full", redactedURL(req.URL))
	span.SetAttribute("server.address", req.URL.Hostname())
	if port, err := strconv.Atoi(req.URL.Port()); err == nil {
		span.setInt("server.port", int64(port))
	}
	// A RoundTripper must not modify the caller's request.
	out := req.Clone(req.Context())
	out.Header.Set("traceparent", span.traceparent())
	resp, err := t.base.RoundTrip(out)
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, err
	}
	span.setInt("http.response.status_code", int64(resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.markError(http.StatusText(resp.StatusCode))
	}
	span.End()
	return resp, nil
}

// redactedURL keeps scheme, host and path; credentials, the query string and
// the fragment can carry secrets.
func redactedURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}
