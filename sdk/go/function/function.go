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
// once, before the init snapshot, and reuses it for later requests, so process
// start and the program's own setup (clients, pools, caches) are paid once
// instead of per request. Older runners that start the handler once per
// request remain supported: they send one envelope and close stdin, and Serve
// answers it and returns.
//
// Inside the runner, stdout is reserved for the protocol. Ordinary writes to
// os.Stdout (fmt.Println, loggers configured with os.Stdout) are redirected to
// stderr, which reaches `gregale logs`.
//
// The envelope carries one value per header. Only the first value of a
// multi-value request header reaches the handler; multi-value response headers
// are joined with ", ", except Set-Cookie, where only the first value is sent.
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

type response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
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
	r := bufio.NewReader(in)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			resp := handle(h, line)
			payload, err := json.Marshal(resp)
			if err != nil {
				return fmt.Errorf("encode response: %w", err)
			}
			if _, err := w.Write(append(payload, '\n')); err != nil {
				return fmt.Errorf("write response: %w", err)
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("write response: %w", err)
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

// handle runs one envelope through h. A malformed envelope or a handler panic
// becomes a 500 handler_error response; the worker keeps serving.
func handle(h http.Handler, line []byte) (resp response) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return handlerError("", fmt.Errorf("decode request envelope: %w", err))
	}
	invocationID := headerValue(env.Headers, "X-Faas-Invocation-Id")
	req, err := newRequest(env)
	if err != nil {
		return handlerError(invocationID, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newRecorder()
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				resp = handlerError(invocationID, errors.New("handler aborted"))
				return
			}
			fmt.Fprintf(os.Stderr, "function: handler panic: %v\n%s", p, debug.Stack())
			resp = handlerError(invocationID, fmt.Errorf("%v", p))
		}
	}()
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec.result()
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
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

// recorder buffers one response. The envelope protocol is not streamed, so
// Flush is accepted and has no effect until the handler returns.
type recorder struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func newRecorder() *recorder { return &recorder{header: http.Header{}} }

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

func (r *recorder) Flush() {}

func (r *recorder) result() response {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	body := r.body.Bytes()
	headers := make(map[string]string, len(r.header)+1)
	for k, values := range r.header {
		if len(values) == 0 {
			continue
		}
		if k == "Set-Cookie" && len(values) > 1 {
			fmt.Fprintln(os.Stderr, "function: only the first Set-Cookie header is sent")
			headers[k] = values[0]
			continue
		}
		headers[k] = strings.Join(values, ", ")
	}
	if _, ok := r.header["Content-Type"]; !ok && len(body) > 0 {
		headers["Content-Type"] = http.DetectContentType(body)
	}
	return response{
		Status:  status,
		Headers: headers,
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}
