package apidserver

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"time"
)

func (s *server) pipelineEventPage(w http.ResponseWriter, r *http.Request, acct state.Account) (api.ExecutionPipelineEventsResponse, bool) {
	access, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return api.ExecutionPipelineEventsResponse{}, false
	}
	store, ok := s.store.(state.ExecutionPipelineProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("pipeline progress unavailable"))
		return api.ExecutionPipelineEventsResponse{}, false
	}
	after, p := executionEventCursor(r)
	if p != nil {
		api.WriteProblem(w, p)
		return api.ExecutionPipelineEventsResponse{}, false
	}
	p, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ExecutionPipelineProgressRetained, api.ExecutionPipelineProgressRetained, "pipeline events")
	if p != nil {
		api.WriteProblem(w, p)
		return api.ExecutionPipelineEventsResponse{}, false
	}
	var principal *string
	if !access.broad {
		principal = access.principal
	}
	page, err := store.ListExecutionPipelineEvents(r.Context(), acct.ID, r.PathValue("id"), principal, after, limit)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return page, false
	}
	return page, true
}
func (s *server) executionPipelineEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	page, ok := s.pipelineEventPage(w, r, acct)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamPipelineProgress(w, r, acct, page)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// Re-authorize each page; stream interruption never cancels the pipeline.
func (s *server) streamPipelineProgress(w http.ResponseWriter, r *http.Request, acct state.Account, page api.ExecutionPipelineEventsResponse) {
	w, ctx, cancel := startSSEStream(w, r)
	defer cancel()
	r = r.Clone(ctx)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	ticker := time.NewTicker(executionEventsPollMax)
	defer ticker.Stop()
	for {
		if page.Gap {
			gapThrough := page.NextSequence
			if len(page.Events) > 0 {
				gapThrough = page.Events[0].Sequence - 1
			}
			cursor, _ := executionEventCursor(r)
			writeJSONPipelineSSE(w, "gap", gapThrough, map[string]any{"after": cursor, "through_sequence": gapThrough, "progress": page.Progress}, flush)
		}
		for _, e := range page.Events {
			writeJSONPipelineSSE(w, e.Type, e.Sequence, e, flush)
		}
		if page.Terminal {
			writeJSONPipelineSSE(w, "terminal", page.NextSequence, map[string]any{"pipeline_id": page.PipelineID, "next_sequence": page.NextSequence}, flush)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		query := r.URL.Query()
		query.Set("after", fmt.Sprint(page.NextSequence))
		r.URL.RawQuery = query.Encode()
		var ok bool
		// Do not write an RFC7807 response after the SSE headers have been sent.
		recorder := &pipelineStreamProblemWriter{header: http.Header{}}
		page, ok = s.pipelineEventPage(recorder, r, acct)
		if !ok {
			writeExecutionSSEError(w, flush, "pipeline access or progress unavailable")
			return
		}
		_, _ = fmt.Fprint(w, ": heartbeat\n\n")
		flush()
	}
}
func writeJSONPipelineSSE(w http.ResponseWriter, kind string, seq int64, v any, flush func()) {
	raw, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, kind, raw)
	flush()
}

type pipelineStreamProblemWriter struct{ header http.Header }

func (w *pipelineStreamProblemWriter) Header() http.Header       { return w.header }
func (*pipelineStreamProblemWriter) WriteHeader(int)             {}
func (*pipelineStreamProblemWriter) Write(b []byte) (int, error) { return len(b), nil }
