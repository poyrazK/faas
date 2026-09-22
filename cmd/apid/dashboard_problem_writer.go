package main

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// dashboardProblemWriter carries request negotiation into shared problem
// writers while preserving the ResponseController and SSE flush paths.
type dashboardProblemWriter struct {
	http.ResponseWriter
	request     *http.Request
	wroteHeader bool
	discardBody bool
}

func (w *dashboardProblemWriter) ProblemHTMLRequest() *http.Request {
	return w.request
}

func (w *dashboardProblemWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *dashboardProblemWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	if status == http.StatusNotFound && api.AcceptsHTML(w.request) && strings.HasPrefix(contentType, "text/plain") {
		w.discardBody = true
		api.WriteProblemForRequest(w.ResponseWriter, w.request, api.NewProblem(
			http.StatusNotFound,
			api.CodeNotFound,
			"Page not found",
			"The requested dashboard page or resource could not be found.",
		).WithHint("Check the address and try again, or return to the dashboard."))
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *dashboardProblemWriter) Write(body []byte) (int, error) {
	if w.discardBody {
		return len(body), nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *dashboardProblemWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

var _ http.Flusher = (*dashboardProblemWriter)(nil)
