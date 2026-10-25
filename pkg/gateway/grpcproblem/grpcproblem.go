// Package grpcproblem answers gRPC callers in gRPC when the gateway refuses
// their request. The gateway's refusals (application authentication, rate
// limits, wake budget, maintenance) are RFC 7807 problem documents; a gRPC
// client cannot read one and reports "server closed the stream without
// sending trailers" instead of the reason (Hunt 14 H14-56). Middleware turns
// any error response to a gRPC request into a trailers-only gRPC response
// that carries the matching status code and the problem's message.
package grpcproblem

import (
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// gRPC status codes (google.golang.org/grpc/codes), kept local so the edge
// does not link grpc for eleven constants.
const (
	codeCancelled          = 1
	codeUnknown            = 2
	codeInvalidArgument    = 3
	codeDeadlineExceeded   = 4
	codeNotFound           = 5
	codePermissionDenied   = 7
	codeResourceExhausted  = 8
	codeFailedPrecondition = 9
	codeAborted            = 10
	codeUnimplemented      = 12
	codeInternal           = 13
	codeUnavailable        = 14
	codeUnauthenticated    = 16
)

const (
	// maxProblemBody bounds how much of a refusal body is read for its
	// message; the rest is discarded.
	maxProblemBody = 16 << 10
	// maxMessage bounds grpc-message before percent-encoding.
	maxMessage = 1024
)

// Middleware translates error responses to gRPC requests. Requests that are
// not gRPC, and responses that are already gRPC, pass through untouched.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsGRPCRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		tw := &writer{ResponseWriter: w}
		next.ServeHTTP(tw, r)
		tw.finish()
	})
}

// IsGRPCRequest reports whether r uses the gRPC wire protocol over HTTP/2.
// gRPC-Web (application/grpc-web) carries its status in the body and is not
// translated.
func IsGRPCRequest(r *http.Request) bool {
	return isGRPCContentType(r.Header.Get("Content-Type"))
}

func isGRPCContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "application/grpc" || strings.HasPrefix(mediaType, "application/grpc+")
}

// CodeForHTTPStatus maps a refusal's HTTP status to the gRPC status a client
// should act on. It follows the gRPC HTTP-to-status table where that table is
// specific, and refines the cases a gateway refusal can name exactly: a rate
// or size limit is RESOURCE_EXHAUSTED, a timeout DEADLINE_EXCEEDED.
func CodeForHTTPStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return codeInvalidArgument
	case http.StatusUnauthorized:
		return codeUnauthenticated
	case http.StatusPaymentRequired, http.StatusPreconditionFailed, http.StatusPreconditionRequired:
		return codeFailedPrecondition
	case http.StatusForbidden:
		return codePermissionDenied
	case http.StatusNotFound, http.StatusGone:
		return codeNotFound
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return codeUnimplemented
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return codeDeadlineExceeded
	case http.StatusConflict:
		return codeAborted
	case http.StatusRequestEntityTooLarge, http.StatusRequestHeaderFieldsTooLarge, http.StatusTooManyRequests:
		return codeResourceExhausted
	case 499: // client closed request
		return codeCancelled
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return codeUnavailable
	case http.StatusInternalServerError:
		return codeInternal
	}
	return codeUnknown
}

type writer struct {
	http.ResponseWriter
	status    int // 0 until a final status is chosen
	translate bool
	body      []byte
}

func (w *writer) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		// Informational responses (100 Continue, 103 Early Hints) precede
		// the final status and never decide it.
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if status >= 400 && !isGRPCContentType(w.Header().Get("Content-Type")) {
		w.translate = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *writer) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.translate {
		return w.ResponseWriter.Write(p)
	}
	if room := maxProblemBody - len(w.body); room > 0 {
		w.body = append(w.body, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// Flush sends nothing while a refusal is being collected: the trailers-only
// response is written once the handler returns.
func (w *writer) Flush() {
	_ = w.FlushError()
}

func (w *writer) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.translate {
		return nil
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the connection for full-duplex
// and deadline control.
func (w *writer) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *writer) finish() {
	if !w.translate {
		return
	}
	h := w.Header()
	for _, key := range []string{"Content-Length", "Content-Encoding", "Content-Language", "Content-Disposition", "Etag", "Last-Modified"} {
		h.Del(key)
	}
	h.Set("Content-Type", "application/grpc")
	h.Set("Grpc-Status", strconv.Itoa(CodeForHTTPStatus(w.status)))
	if message := refusalMessage(w.status, w.body); message != "" {
		h.Set("Grpc-Message", EncodeMessage(message))
	}
	// Trailers-only: HTTP 200 and no body, the status in the headers.
	w.ResponseWriter.WriteHeader(http.StatusOK)
}

// refusalMessage names the refusal from its problem document when it is one,
// else from the HTTP status, so gRPC clients see the gateway's reason.
func refusalMessage(status int, body []byte) string {
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	message := http.StatusText(status)
	if json.Unmarshal(body, &problem) == nil {
		title, detail := strings.TrimSpace(problem.Title), strings.TrimSpace(problem.Detail)
		switch {
		case title != "" && detail != "":
			message = title + ": " + detail
		case title != "":
			message = title
		case detail != "":
			message = detail
		}
		if code := strings.TrimSpace(problem.Code); code != "" {
			message += " (" + code + ")"
		}
	}
	if len(message) > maxMessage {
		message = message[:maxMessage]
	}
	return message
}

// EncodeMessage percent-encodes a grpc-message value as the gRPC HTTP/2
// protocol requires: every byte outside printable ASCII, and '%', becomes
// %XX.
func EncodeMessage(message string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(message); i++ {
		c := message[i]
		if c >= 0x20 && c <= 0x7e && c != '%' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}
