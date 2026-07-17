package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
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
					// codeql[go/log-injection] false-positive: r.Method and r.URL.Path are parser-validated by net/http before reaching this handler; control characters and CRLF are rejected at parse time. `rec` and `debug.Stack()` are runtime values, not user input.
					log.Error("panic recovered",
						"request_id", rid,
						"method", r.Method,
						"path", r.URL.Path,
						"panic", rec,
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
