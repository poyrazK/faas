package gateway

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// bufferReplayableServiceBody makes a small service-call body rewindable so
// the proxy can replay it: the ADR-1133 re-route after an undelivered failure
// and ordinary ADR-201 retries both need GetBody.
//
// Only bodies that declare a Content-Length of at most
// ServiceProxyReplayBodyMaxBytes are read up front. Chunked or unknown-length
// bodies may be long-lived streams, so waiting for EOF could stall the call;
// they, larger bodies, Upgrade requests and native gRPC streams are forwarded
// as before and simply stay non-replayable.
//
// It reports false after writing a problem when the declared body could not be
// read (the caller aborted or sent fewer bytes than it declared).
func bufferReplayableServiceBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody || r.GetBody != nil || isUpgradeRequest(r) {
		return true
	}
	if r.ContentLength < 0 || r.ContentLength > api.ServiceProxyReplayBodyMaxBytes {
		return true
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil &&
		(mediaType == "application/grpc" || strings.HasPrefix(mediaType, "application/grpc+")) {
		return true // ADR-428: native gRPC exchanges messages before request EOF
	}
	// Grow with the bytes received: allocating the declared length up front
	// let a caller pin the whole buffer by declaring a body and stalling.
	body, err := io.ReadAll(io.LimitReader(r.Body, r.ContentLength))
	_ = r.Body.Close()
	if err == nil && int64(len(body)) < r.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		api.WriteProblem(w, api.ErrRequestBodyReadFailed())
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return true
}
