package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// gRPC callers and platform errors.
//
// A gRPC client reads the call's outcome from the grpc-status trailer. When
// the gateway, vmmd or the stream bridge could not complete a call it answered
// with an HTTP error and a problem document (or plain text), and gRPC clients
// turn that into confusing errors: grpc-go reports "server closed the stream
// without sending trailers" (code Internal), which neither names the cause
// nor counts as retryable (microservices-demo, 2026-11-16).
//
// grpcPlatformErrorWriter wraps the outermost writer of a gRPC request. An
// error response that is not itself gRPC (status >= 400 without an
// application/grpc content type) becomes a gRPC answer: HTTP 200,
// content-type application/grpc, no body, and grpc-status/grpc-message sent
// as trailers, with the status mapped as gRPC's HTTP-to-gRPC table maps it
// (502/503/504/429 are UNAVAILABLE, which clients may retry). A response the
// app produced as gRPC passes through untouched. Writers inside the wrapper,
// including request telemetry, still see the original HTTP status.

// grpcErrorBodyLimit bounds the error body kept to build grpc-message.
const grpcErrorBodyLimit = 4 << 10

// isGRPCRequest reports whether r is native gRPC (not gRPC-Web, whose
// trailers travel in the body).
func isGRPCRequest(r *http.Request) bool {
	return isGRPCContentType(r.Header.Get("Content-Type"))
}

func isGRPCContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if !strings.HasPrefix(ct, "application/grpc") {
		return false
	}
	rest := ct[len("application/grpc"):]
	return rest == "" || rest[0] == '+' || rest[0] == ';'
}

// grpcStatusForHTTP maps an HTTP status to a gRPC status code
// (grpc/doc/http-grpc-status-mapping.md).
func grpcStatusForHTTP(status int) int {
	switch status {
	case http.StatusBadRequest:
		return 13 // INTERNAL
	case http.StatusUnauthorized:
		return 16 // UNAUTHENTICATED
	case http.StatusForbidden:
		return 7 // PERMISSION_DENIED
	case http.StatusNotFound:
		return 12 // UNIMPLEMENTED
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return 14 // UNAVAILABLE
	default:
		return 2 // UNKNOWN
	}
}

type grpcPlatformErrorWriter struct {
	http.ResponseWriter
	wroteHeader bool
	converted   bool
	status      int
	body        bytes.Buffer
}

// wrapGRPCPlatformErrors returns w wrapped for a gRPC request and a finish
// func the handler defers; for any other request it returns w unchanged.
func wrapGRPCPlatformErrors(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if !isGRPCRequest(r) {
		return w, func() {}
	}
	if existing, ok := w.(*grpcPlatformErrorWriter); ok {
		return existing, func() {}
	}
	gw := &grpcPlatformErrorWriter{ResponseWriter: w}
	return gw, gw.finish
}

func (w *grpcPlatformErrorWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status < http.StatusBadRequest || isGRPCContentType(w.Header().Get("Content-Type")) {
		if status >= http.StatusOK {
			w.wroteHeader = true
		}
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	w.converted = true
	w.status = status
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Content-Encoding")
	h.Set("Content-Type", "application/grpc")
	h.Set("X-Gregale-Http-Status", strconv.Itoa(status))
	w.ResponseWriter.WriteHeader(http.StatusOK)
}

func (w *grpcPlatformErrorWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.converted {
		return w.ResponseWriter.Write(p)
	}
	if room := grpcErrorBodyLimit - w.body.Len(); room > 0 {
		w.body.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// finish sets the trailers of a converted response. It must run before the
// handler returns: net/http reads trailers when the handler ends.
func (w *grpcPlatformErrorWriter) finish() {
	if !w.converted {
		return
	}
	h := w.Header()
	h.Set(http.TrailerPrefix+"Grpc-Status", strconv.Itoa(grpcStatusForHTTP(w.status)))
	h.Set(http.TrailerPrefix+"Grpc-Message", grpcPercentEncode(grpcErrorMessage(w.status, w.body.Bytes())))
}

// grpcErrorMessage names the platform's reason: the problem document's code
// and detail when the body is one, else its first line of text.
func grpcErrorMessage(status int, body []byte) string {
	msg := "gregale: " + strconv.Itoa(status) + " " + http.StatusText(status)
	var problem struct {
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &problem) == nil && (problem.Code != "" || problem.Detail != "" || problem.Title != "") {
		parts := []string{msg}
		if problem.Code != "" {
			parts = append(parts, problem.Code)
		}
		if text := firstNonEmpty(problem.Detail, problem.Title); text != "" {
			parts = append(parts, text)
		}
		return strings.Join(parts, ": ")
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(body)), "\n")
	if line = strings.TrimSpace(line); line != "" {
		return msg + ": " + line
	}
	return msg
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// grpcPercentEncode encodes grpc-message as the gRPC spec requires: bytes
// outside printable ASCII and '%' are percent-encoded.
func grpcPercentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c <= 0x7e && c != '%' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (w *grpcPlatformErrorWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *grpcPlatformErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("gateway: response does not support hijacking")
}

func (w *grpcPlatformErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
