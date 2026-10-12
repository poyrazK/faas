// Package function runs a standard net/http handler as a Gregale go124
// function.
//
//	func main() {
//		function.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			fmt.Fprintf(w, "hello from %s\n", r.URL.Path)
//		}))
//	}
//
// The go124 runner owns the HTTP listener inside the microVM and talks to the
// handler binary over stdin/stdout with newline-framed JSON envelopes. Serve
// translates each envelope into an *http.Request and the handler's output back
// into a response envelope, so routers such as http.ServeMux, chi or gin work
// unchanged.
//
// A binary built with Serve stays alive between requests: the runner starts it
// once per instance and reuses it for later requests, so process start and the
// program's own setup (clients, pools, caches) are paid once instead of per
// request. After a snapshot restore the runner starts a fresh process, so no
// random state is shared between restored instances. Older runners that start the handler once per
// request remain supported: they send one envelope and close stdin, and Serve
// answers it and returns.
//
// Inside the runner, stdout is reserved for the protocol. Ordinary writes to
// os.Stdout (fmt.Println, loggers configured with os.Stdout) are redirected to
// stderr, which reaches `gregale logs`.
//
// A handler that calls Flush (through http.Flusher or http.ResponseController)
// streams: the status and headers go out at the first Flush and each later
// Flush sends the body written since, so server-sent events and long outputs
// reach the client as they are produced. A handler that never flushes sends
// one complete response, as before. Every value of a response header is sent,
// several Set-Cookie headers included; a repeated request header arrives as
// one value joined with ", " ("; " for Cookie).
package function

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
)

// protocolMarker advertises the persistent protocol to the go124 runner. The
// runner looks for these bytes in the compiled binary; referencing the marker
// from Serve keeps it in every binary that can reach Serve.
const protocolMarker = "FAAS_PERSISTENT_PROTOCOL_V1"

const readyLine = `{"__faas_ready":true,"protocol":"` + protocolMarker + `"}`

// protocolOut is the protocol channel to the runner. Inside the runner it is
// the original stdout and os.Stdout is redirected to stderr; elsewhere (local
// runs, tests) it is os.Stdout.
var protocolOut io.Writer = os.Stdout

func init() {
	if os.Getenv("FAAS_RUNTIME") != "" {
		protocolOut = reserveStdout()
	}
}

type envelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
}

// response is one line to the runner: a complete response, or with Frame a
// streamed response's head, a body chunk, its end or an error (ADR-999).
type response struct {
	Frame   string         `json:"frame,omitempty"`
	Status  int            `json:"status,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
	BodyB64 string         `json:"body_b64,omitempty"`
	Message string         `json:"message,omitempty"`
}

// frameWriter sends response lines to the runner. The first failure is kept
// and stops further writes; serve returns it.
type frameWriter struct {
	w   *bufio.Writer
	err error
}

func (f *frameWriter) emit(resp response) {
	if f.err != nil {
		return
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		f.err = fmt.Errorf("encode response: %w", err)
		return
	}
	if _, err := f.w.Write(append(payload, '\n')); err != nil {
		f.err = fmt.Errorf("write response: %w", err)
		return
	}
	if err := f.w.Flush(); err != nil {
		f.err = fmt.Errorf("write response: %w", err)
	}
}

// Serve answers runner requests with h until the runner closes stdin. It does
// not return on a protocol I/O failure; it exits the process with status 1 so
// the runner replaces the worker.
func Serve(h http.Handler) {
	persistent := os.Getenv("FAAS_PERSISTENT_WORKER") == "1"
	if err := serve(h, os.Stdin, protocolOut, persistent); err != nil {
		fmt.Fprintf(os.Stderr, "function: %v\n", err)
		os.Exit(1)
	}
}

func serve(h http.Handler, in io.Reader, out io.Writer, persistent bool) error {
	w := bufio.NewWriter(out)
	if persistent {
		if _, err := w.WriteString(readyLine + "\n"); err != nil {
			return fmt.Errorf("write ready handshake: %w", err)
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("write ready handshake: %w", err)
		}
	}
	frames := &frameWriter{w: w}
	r := bufio.NewReader(in)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			handle(h, line, frames)
			if frames.err != nil {
				return frames.err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read request: %w", readErr)
		}
	}
}

// handle runs one envelope through h and sends its response. A malformed
// envelope or a handler panic becomes a 500 handler_error response, or an
// error frame once a stream has started; the worker keeps serving.
func handle(h http.Handler, line []byte, out *frameWriter) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		out.emit(handlerError("", fmt.Errorf("decode request envelope: %w", err)))
		return
	}
	invocationID := headerValue(env.Headers, "X-Faas-Invocation-Id")
	req, err := newRequest(env)
	if err != nil {
		out.emit(handlerError(invocationID, err))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newRecorder(out)
	defer func() {
		if p := recover(); p != nil {
			failure := errors.New("handler aborted")
			if p != http.ErrAbortHandler {
				fmt.Fprintf(os.Stderr, "function: handler panic: %v\n%s", p, debug.Stack())
				failure = fmt.Errorf("%v", p)
			}
			rec.fail(invocationID, failure)
		}
	}()
	h.ServeHTTP(rec, req.WithContext(ctx))
	rec.finish()
}

func newRequest(env envelope) (*http.Request, error) {
	body, err := base64.StdEncoding.DecodeString(env.BodyB64)
	if err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	method := env.Method
	if method == "" {
		method = http.MethodPost
	}
	path := env.Path
	if path == "" {
		path = "/"
	}
	target := path
	if q := strings.TrimPrefix(env.Query, "?"); q != "" {
		target += "?" + q
	}
	host := headerValue(env.Headers, "Host")
	if host == "" {
		host = "faas.local"
	}
	req, err := http.NewRequest(method, "http://"+host+target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.RequestURI = target
	for k, v := range env.Headers {
		if strings.EqualFold(k, "Host") {
			continue
		}
		req.Header[http.CanonicalHeaderKey(k)] = []string{v}
	}
	if len(body) == 0 && (method == http.MethodGet || method == http.MethodHead) {
		req.Body = http.NoBody
	}
	return req, nil
}

func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func handlerError(invocationID string, err error) response {
	message := err.Error()
	if len(message) > 256 {
		message = message[:256] + "…"
	}
	body, _ := json.Marshal(map[string]string{
		"error":         "handler_error",
		"message":       message,
		"invocation_id": invocationID,
	})
	return response{
		Status:  http.StatusInternalServerError,
		Headers: map[string]any{"Content-Type": "application/json; charset=utf-8"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

// recorder collects one response. Until the handler flushes it is buffered
// and sent whole; the first Flush sends the status and headers, and each Flush
// after that sends the body written since (ADR-999).
type recorder struct {
	out         *frameWriter
	header      http.Header
	status      int
	wroteHeader bool
	streaming   bool
	body        bytes.Buffer
}

func newRecorder(out *frameWriter) *recorder { return &recorder{out: out, header: http.Header{}} }

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader || status < 200 {
		return
	}
	r.wroteHeader = true
	r.status = status
}

func (r *recorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(p)
}

// Flush streams what the handler has written so far.
func (r *recorder) Flush() {
	if !r.streaming {
		r.streaming = true
		status, headers := r.head()
		r.out.emit(response{Frame: "head", Status: status, Headers: headers})
	}
	r.sendChunk()
}

func (r *recorder) sendChunk() {
	if r.body.Len() == 0 {
		return
	}
	r.out.emit(response{Frame: "chunk", BodyB64: base64.StdEncoding.EncodeToString(r.body.Bytes())})
	r.body.Reset()
}

// finish sends the response once the handler returns.
func (r *recorder) finish() {
	if r.streaming {
		r.sendChunk()
		r.out.emit(response{Frame: "end"})
		return
	}
	status, headers := r.head()
	r.out.emit(response{Status: status, Headers: headers, BodyB64: base64.StdEncoding.EncodeToString(r.body.Bytes())})
}

// fail reports a handler failure: a 500 when nothing has been sent, or an
// error frame that aborts a stream already under way.
func (r *recorder) fail(invocationID string, err error) {
	if r.streaming {
		r.out.emit(response{Frame: "error", Message: err.Error()})
		return
	}
	r.out.emit(handlerError(invocationID, err))
}

// head is the response status and headers. A header with several values is
// sent as a list, one header line each.
func (r *recorder) head() (int, map[string]any) {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := make(map[string]any, len(r.header))
	for k, values := range r.header {
		switch len(values) {
		case 0:
		case 1:
			headers[k] = values[0]
		default:
			headers[k] = append([]string(nil), values...)
		}
	}
	if _, ok := r.header["Content-Type"]; !ok && r.body.Len() > 0 {
		headers["Content-Type"] = http.DetectContentType(r.body.Bytes())
	}
	return status, headers
}
