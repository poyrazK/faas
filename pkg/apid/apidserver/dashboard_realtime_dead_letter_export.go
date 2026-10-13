package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppRealtimeDeadLetterExportPath(rest string) (string, bool) {
	const suffix = "/realtime/dead-letters/export"
	rest = strings.TrimSuffix(rest, "/")
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	return slug, slug != "" && !strings.Contains(slug, "/")
}

func (s *server) renderRealtimeDeadLetterExport(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	for _, key := range []string{"endpoint_id", "channel", "group"} {
		if values := q[key]; len(values) != 1 || values[0] == "" {
			api.WriteProblem(w, api.ErrRealtimeInvalid("choose one endpoint, channel and consumer group"))
			return
		}
	}
	req := r.Clone(r.Context())
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", q.Get("endpoint_id"))
	req.SetPathValue("channel", q.Get("channel"))
	req.SetPathValue("group", q.Get("group"))
	s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.realtime.ExportRealtimeConsumerGroupDeadLetters))(w, req, acct)
}
