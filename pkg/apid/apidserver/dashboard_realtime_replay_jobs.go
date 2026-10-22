package apidserver

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (s *server) renderRealtimeReplayJobAction(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid replay form"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, "realtime_replay_job", acct.ID, "faas_replay_job_csrf"); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid CSRF token"))
		return
	}
	req := r.Clone(r.Context())
	req.SetPathValue("slug", slug)
	for key, path := range map[string]string{"endpoint_id": "id", "channel": "channel", "group": "group"} {
		values := r.URL.Query()[key]
		if len(values) != 1 || values[0] == "" {
			api.WriteProblem(w, api.ErrRealtimeInvalid("select an endpoint, channel and group"))
			return
		}
		req.SetPathValue(path, values[0])
	}
	req.SetPathValue("job", r.PostForm.Get("job"))
	req.SetPathValue("action", r.PostForm.Get("action"))
	if req.PathValue("job") == "" {
		rate, err := strconv.Atoi(r.PostForm.Get("rate"))
		if err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid replay rate"))
			return
		}
		var codes []string
		if value := strings.TrimSpace(r.PostForm.Get("failure_codes")); value != "" {
			codes = strings.Fields(value)
		}
		body, _ := json.Marshal(state.RealtimeReplayJobRequest{Rate: rate, FailureCodes: codes, AfterSequence: r.PostForm.Get("after_sequence"), ThroughSequence: r.PostForm.Get("through_sequence")})
		req.Body = io.NopCloser(bytes.NewReader(body))
		if r.PathValue("mode") == "dry_run" {
			req.SetPathValue("mode", "dry_run")
		}
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}
	s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardReplayJobMutation))(w, req, acct)
}
func (s *server) dashboardReplayJobMutation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	result := &replayJobRedirectWriter{ResponseWriter: w}
	s.realtime.RealtimeReplayJobs(result, r, acct)
	if result.success {
		w.Header().Del("Content-Type")
		http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(r.PathValue("slug"))+"/realtime?"+url.Values{"endpoint_id": {r.PathValue("id")}, "channel": {r.PathValue("channel")}, "group": {r.PathValue("group")}}.Encode(), http.StatusSeeOther)
	}
}

type replayJobRedirectWriter struct {
	http.ResponseWriter
	success bool
}

func (w *replayJobRedirectWriter) WriteHeader(code int) {
	if code >= 200 && code < 300 {
		w.success = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *replayJobRedirectWriter) Write(value []byte) (int, error) {
	if w.success {
		return len(value), nil
	}
	return w.ResponseWriter.Write(value)
}
