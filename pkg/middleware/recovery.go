package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Recovery converts a panic in downstream handlers into a 500 RFC 7807
// response + structured slog.Error with the stack. Required by spec
// §11: "nothing reachable from the public listener may crash the
// process" — a template panic or nil-deref must not take apid down.
//
// log may be nil (slog.Default is used).
// requestID may be empty.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
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
					// The response writer may already be partially written;
					// only attempt to write the problem if we can.
					if rw, ok := w.(interface{ Headers() http.Header }); ok {
						_ = rw // type assertion kept for future hints
					}
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"type":"about:blank","title":"internal","status":500,"detail":"internal server error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
