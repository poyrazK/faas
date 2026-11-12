package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prefix flag and active recorder are invocation wiring. Clients capture the
// recorder when constructed; the recorder is closed when that run returns.
var httpDebugEnabled bool
var activeHTTPDebug atomic.Pointer[httpDebugRecorder]

type httpDebugRecorder struct {
	mu     sync.Mutex
	out    io.Writer
	next   uint64
	closed bool
}

type httpDebugRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Event         string `json:"event"`
	Request       uint64 `json:"request_seq"`
	Method        string `json:"method"`
	Route         string `json:"route"`
	At            string `json:"at"`
	HeadersMS     *int64 `json:"headers_ms,omitempty"`
	Status        int    `json:"status_code,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	ErrorKind     string `json:"error_kind,omitempty"`
}

type httpDebugTicket struct {
	id            uint64
	method, route string
	started       time.Time
}

func (r *httpDebugRecorder) start(req *http.Request) httpDebugTicket {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return httpDebugTicket{}
	}
	r.next++
	ticket := httpDebugTicket{id: r.next, method: httpDebugMethod(req.Method), route: "/[redacted]"}
	if req.URL != nil {
		ticket.route = httpDebugRoute(req.URL.Path)
	}
	r.emitLocked(httpDebugRecord{Event: "request_start", Request: ticket.id, Method: ticket.method, Route: ticket.route})
	ticket.started = time.Now()
	return ticket
}

func (r *httpDebugRecorder) finish(ticket httpDebugTicket, resp *http.Response, err error, ctx context.Context) {
	if ticket.id == 0 {
		return
	}
	elapsed := time.Since(ticket.started).Milliseconds()
	record := httpDebugRecord{Event: "response_headers", Request: ticket.id, Method: ticket.method, Route: ticket.route, HeadersMS: &elapsed}
	if err != nil {
		record.Event, record.ErrorKind = "request_error", httpDebugErrorKind(ctx, err)
	} else if resp == nil {
		record.Event, record.ErrorKind = "request_error", "missing_response"
	} else {
		record.Status = resp.StatusCode
		// Only recognized opaque identifier shapes are copied. Arbitrary header
		// text, including error bodies and credential-like values, stays out.
		if values := resp.Header.Values(api.RequestIDHeader); len(values) == 1 && httpDebugRequestID.MatchString(values[0]) {
			record.RequestID = values[0]
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.emitLocked(record)
	}
}

func (r *httpDebugRecorder) emitLocked(record httpDebugRecord) {
	record.SchemaVersion, record.Type, record.At = 1, "http_debug", time.Now().UTC().Format(time.RFC3339Nano)
	if err := json.NewEncoder(r.out).Encode(record); err != nil {
		r.closed = true
	}
}

func (r *httpDebugRecorder) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}

var httpDebugRequestID = regexp.MustCompile(`^(?:[A-Fa-f0-9]{32}|[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12})$`)

func httpDebugMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

// Return constant labels only. No request segment, URL, query, host, fragment
// or userinfo is copied into a debug record. Unknown routes retain a coarse
// allowlisted resource family, or the fully redacted fallback.
func httpDebugRoute(path string) string {
	parts := strings.Split(path, "/")
	for _, pattern := range []string{
		"/v1/capabilities", "/v1/account", "/v1/me", "/v1/usage", "/v1/usage/summary", "/v1/plan",
		"/v1/apps", "/v1/apps/{app}", "/v1/apps/{app}/status", "/v1/apps/{app}/logs",
		"/v1/apps/{app}/deployments", "/v1/apps/{app}/secrets", "/v1/apps/{app}/secrets/{key}",
		"/v1/apps/{app}/env", "/v1/apps/{app}/env/{key}",
		"/v1/deployments/{deployment}", "/v1/deployments/{deployment}/logs",
		"/v1/projects", "/v1/projects/{project}", "/v1/projects/{project}/environments",
		"/v1/projects/{project}/environments/{environment}",
		"/v1/jobs", "/v1/jobs/{job}", "/v1/invocations/{invocation}",
	} {
		segments := strings.Split(pattern, "/")
		if len(parts) != len(segments) {
			continue
		}
		matched := true
		for i, segment := range segments {
			if strings.HasPrefix(segment, "{") {
				matched = matched && parts[i] != ""
			} else {
				matched = matched && parts[i] == segment
			}
		}
		if matched {
			return pattern
		}
	}
	if len(parts) >= 3 && parts[0] == "" && parts[1] == "v1" {
		switch parts[2] {
		case "apps", "deployments", "projects", "jobs", "invocations", "workflows", "queues", "postgres", "databases", "buckets", "auth", "account", "accounts", "tokens", "github", "approvals", "agents", "logdrains", "support":
			return "/v1/" + parts[2] + "/{...}"
		}
	}
	return "/[redacted]"
}

func httpDebugErrorKind(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var unknown x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	var handshake tls.RecordHeaderError
	if errors.As(err, &unknown) || errors.As(err, &certificate) || errors.As(err, &hostname) || errors.As(err, &handshake) {
		return "tls"
	}
	var connection *net.OpError
	if errors.As(err, &connection) {
		return "connection"
	}
	return "transport"
}
