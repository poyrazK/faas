package httpkit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type replayRecorder struct {
	http.ResponseWriter
	skipped bool
}

func (r *replayRecorder) SkipIdempotentReplay() { r.skipped = true }

type unwrappingWriter struct{ http.ResponseWriter }

func (u unwrappingWriter) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// adr: 1330
func TestSkipIdempotentReplayFindsTheRecorder(t *testing.T) {
	direct := &replayRecorder{ResponseWriter: httptest.NewRecorder()}
	SkipIdempotentReplay(direct)
	wrapped := &replayRecorder{ResponseWriter: httptest.NewRecorder()}
	SkipIdempotentReplay(unwrappingWriter{unwrappingWriter{wrapped}})
	if !direct.skipped || !wrapped.skipped {
		t.Fatalf("direct=%v wrapped=%v; want both marked", direct.skipped, wrapped.skipped)
	}
	// Without a recorder in the chain the call is a no-op.
	SkipIdempotentReplay(httptest.NewRecorder())
	SkipIdempotentReplay(unwrappingWriter{httptest.NewRecorder()})
	SkipIdempotentReplay(nil)
}
