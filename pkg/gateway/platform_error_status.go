package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// platformProblemBodyLimit bounds the body PlatformErrorStatus holds to
// correct a problem's status member. Gregale's problems are a few hundred
// bytes; a larger body is sent as written.
const platformProblemBodyLimit = 64 << 10

// PlatformErrorStatus sends a platform problem's 502 or 504 as 503
// (ADR-20261030115139). Cloudflare replaces an origin's 502/504 body with its
// own page and passes 503 through, so the problem's body, code and request id
// reach the client. A platform problem is a response that carries
// api.ErrorCodeHeader: api.WriteProblem stamps it, and gatewayd-internal drops
// a guest's copy, so an app's own 502/504 is left alone. A problem+json body's
// status member is corrected to match (RFC 9457). Layers inside this one keep
// the original status in their metrics and logs.
func PlatformErrorStatus(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := &platformErrorWriter{ResponseWriter: w}
		next.ServeHTTP(pw, r)
		pw.finish()
	})
}

type platformErrorWriter struct {
	http.ResponseWriter
	wroteHeader bool
	// holding is set while a remapped problem+json body is held so its
	// status member can be corrected; held is that body.
	holding bool
	status  int
	held    bytes.Buffer
}

func (w *platformErrorWriter) WriteHeader(code int) {
	if w.holding {
		return
	}
	if w.wroteHeader || code < http.StatusOK {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.wroteHeader = true
	if (code == http.StatusBadGateway || code == http.StatusGatewayTimeout) && w.Header().Get(api.ErrorCodeHeader) != "" {
		if mediaType, _, err := mime.ParseMediaType(w.Header().Get("Content-Type")); err == nil && mediaType == "application/problem+json" {
			w.holding = true
			w.status = code
			return
		}
		code = http.StatusServiceUnavailable
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *platformErrorWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.holding {
		return w.ResponseWriter.Write(p)
	}
	if w.held.Len()+len(p) > platformProblemBodyLimit {
		w.release(false)
		return w.ResponseWriter.Write(p)
	}
	return w.held.Write(p)
}

// release sends the held response as 503, correcting the body's status member
// when correct is set.
func (w *platformErrorWriter) release(correct bool) {
	w.holding = false
	body := w.held.Bytes()
	if correct {
		if fixed := problemWithStatus(body, w.status, http.StatusServiceUnavailable); !bytes.Equal(fixed, body) {
			body = fixed
			if w.Header().Get("Content-Length") != "" {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
		}
	}
	w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
	if len(body) > 0 {
		_, _ = w.ResponseWriter.Write(body)
	}
	w.held.Reset()
}

func (w *platformErrorWriter) finish() {
	if w.holding {
		w.release(true)
	}
}

// problemWithStatus returns body with its top-level status member changed from
// one status to another, or body unchanged when it is not a JSON object whose
// status member is from.
func problemWithStatus(body []byte, from, to int) []byte {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		return body
	}
	var status int
	if raw, ok := members["status"]; !ok || json.Unmarshal(raw, &status) != nil || status != from {
		return body
	}
	members["status"] = json.RawMessage(strconv.Itoa(to))
	out, err := json.Marshal(members)
	if err != nil {
		return body
	}
	if bytes.HasSuffix(body, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

// Unwrap preserves connection controls such as write deadlines.
func (w *platformErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// FlushError sends a held problem before flushing.
func (w *platformErrorWriter) FlushError() error {
	if w.holding {
		w.release(true)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush supports handlers that use the legacy http.Flusher interface.
func (w *platformErrorWriter) Flush() { _ = w.FlushError() }

// Hijack supports protocol upgrades through handlers that assert
// http.Hijacker directly.
func (w *platformErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
