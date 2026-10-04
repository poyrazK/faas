package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// startSSEStream commits the SSE response headers and returns the writer and
// context a long-lived stream must use from then on.
//
// apid's request-budget middleware cancels r.Context() after
// api.RequestBudgetApidDefault (5 s). A stream that waits on r.Context()
// therefore closed every SSE connection five seconds after it opened, and
// `gregale tail` exited. The returned context detaches from that admission
// budget once the headers are committed (reqbudget.WithStream); it still ends
// when the client disconnects or the server shuts down.
//
// The returned writer also replaces the listener's fixed WriteTimeout
// (api.APIDWriteTimeoutSecondsDefault, measured from request start) with a
// per-write deadline, so an attached stream is not cut at five minutes while
// a client that stops reading still releases the handler.
func startSSEStream(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, context.Context, context.CancelFunc) {
	ctx, detach, cancel := reqbudget.WithStream(r.Context())
	stream := &sseStreamWriter{ResponseWriter: w, rc: http.NewResponseController(w)}
	stream.extendDeadline()
	apislogs.StartSSE(stream)
	detach()
	return stream, ctx, cancel
}

// sseStreamWriter extends the connection write deadline before every write.
type sseStreamWriter struct {
	http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseStreamWriter) extendDeadline() {
	_ = s.rc.SetWriteDeadline(time.Now().Add(api.APIDStreamWriteTimeout))
}

func (s *sseStreamWriter) Write(p []byte) (int, error) {
	s.extendDeadline()
	return s.ResponseWriter.Write(p)
}

func (s *sseStreamWriter) Flush() {
	s.extendDeadline()
	_ = s.rc.Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *sseStreamWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
