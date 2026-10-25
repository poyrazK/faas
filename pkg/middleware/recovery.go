package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Recovery converts a panic in downstream handlers into a 500 RFC 7807
// response + structured slog.Error with the stack. Required by spec
// §11: "nothing reachable from the public listener may crash the
// process" — a template panic or nil-deref must not take apid down.
//
// Two panics are not turned into a 500:
//   - http.ErrAbortHandler is re-raised untouched. net/http and
//     httputil.ReverseProxy use it to abort a response on purpose.
//   - A panic after the handler started its response (status ≥ 200 or
//     body bytes written) is logged and then re-raised as
//     http.ErrAbortHandler, so net/http drops the connection. Writing a
//     problem body there would append JSON to a partial stream, and a
//     clean end of stream would let the client treat a truncated body as
//     complete.
//
// log may be nil (slog.Default is used).
// requestID may be empty.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := &recoveryWriter{ResponseWriter: w}
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler { //nolint:errorlint // net/http compares the sentinel by identity.
						panic(rec)
					}
					rid := RequestIDFrom(r)
					// CodeQL go/log-injection (CWE-117): `rec` is the
					// interface{} returned by recover(). Whatever a
					// handler panicked with (string, error, custom
					// type, anything) routes through logsanitize.FieldAny
					// so a hostile panic payload cannot inject CR/LF
					// and forge a new log line. The stack trace is
					// internal-only (runtime/debug output) and safe.
					log.Error("panic recovered",
						"request_id", rid,
						"method", logsanitize.Field(r.Method),
						"path", logsanitize.Field(r.URL.Path),
						"panic", logsanitize.FieldAny(rec),
						"stack", string(debug.Stack()),
					)
					if rw.started {
						panic(http.ErrAbortHandler)
					}
					api.WriteProblemForRequest(w, r, api.ErrInternal(
						"Gregale encountered an unexpected error while processing this request.",
					).WithHint("Retry the request; if it still fails, contact support."))
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

// recoveryWriter records whether the final response has started so
// Recovery knows if a problem body can still be written.
type recoveryWriter struct {
	http.ResponseWriter
	started bool
}

func (w *recoveryWriter) WriteHeader(status int) {
	if status >= http.StatusOK {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveryWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

// Flush keeps http.Flusher visible to SSE handlers that assert it
// directly. Flushing commits the headers, so the response has started.
func (w *recoveryWriter) Flush() {
	w.started = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack keeps http.Hijacker visible to WebSocket upgraders that assert
// it directly (gorilla/websocket v1.5.3). A hijacked connection belongs
// to the handler, so the response counts as started.
func (w *recoveryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.started = true
	}
	return conn, brw, err
}

// Unwrap lets http.ResponseController reach the underlying writer's
// optional interfaces (deadlines, full duplex).
func (w *recoveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
