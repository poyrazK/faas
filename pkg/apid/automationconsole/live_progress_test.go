// adr: 1293
package automationconsole

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestLiveProgressReadsOnlySafeSelectedResult(t *testing.T) {
	host, id := inspectionFixture(t)
	host.responses["live-get"] = inspectionJSON(t, api.AutomationLiveVerification{RunID: id, CaseName: "synthetic", RunStatus: "pending"})
	service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := httptest.NewRequest(http.MethodGet, "/?name=report&action=run&input=PRIVATE", nil)
	r.SetPathValue("run_id", id)
	w := httptest.NewRecorder()
	service.LiveProgress(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `data-live-state="pending"`) {
		t.Fatalf("progress read: %d %s", w.Code, w.Body.String())
	}
	if len(host.calls) != 1 || host.calls[0].Kind != "live-get" || host.calls[0].Name != "report" || host.calls[0].RunID != id {
		t.Fatalf("progress read requested extra work: %+v", host.calls)
	}
	for _, forbidden := range []string{"PRIVATE", "automation-definition", "<script", "csrf_token", "confirm_live"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("progress fragment contains %q", forbidden)
		}
	}
}

func TestLiveProgressCheckAcceptsOnlyTargetAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		name string
		form url.Values
		ok   bool
	}{
		{"token only", url.Values{"csrf_token": {"test-csrf"}}, true},
		{"input override", url.Values{"csrf_token": {"test-csrf"}, "input": {"PRIVATE"}}, false},
		{"execution action", url.Values{"csrf_token": {"test-csrf"}, "action": {"run"}}, false},
		{"oversized", url.Values{"csrf_token": {strings.Repeat("x", int(api.AutomationLiveProgressFormMaxBytes)+1)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, id := inspectionFixture(t)
			host.responses["live-check"] = inspectionJSON(t, api.AutomationLiveVerification{RunID: id, CaseName: "synthetic", RunStatus: "succeeded",
				ReportCurrent: true, Report: &api.AutomationLiveReport{Passed: true, CheckedAt: time.Now()}})
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			r := reportRequest(tc.form)
			r.URL.RawQuery = "name=report"
			r.SetPathValue("run_id", id)
			w := httptest.NewRecorder()
			service.CheckLiveProgress(w, r)
			if tc.ok {
				if w.Code != 200 || len(host.calls) != 1 || host.calls[0].Kind != "live-check" || host.calls[0].Body != nil {
					t.Fatalf("result check requested unexpected work: %d %+v", w.Code, host.calls)
				}
			} else if w.Code != 400 || len(host.calls) != 0 {
				t.Fatalf("invalid result check reached backend: %d %+v", w.Code, host.calls)
			}
		})
	}
}

func TestLiveProgressRejectsWrongRunAndHidesBackendErrors(t *testing.T) {
	for _, response := range []Response{
		{Status: 503, Body: []byte(`{"detail":"PRIVATE-error"}`)},
		inspectionJSON(t, api.AutomationLiveVerification{RunID: uuid.NewString(), CaseName: "PRIVATE-wrong-run"}),
	} {
		host, id := inspectionFixture(t)
		host.responses["live-get"] = response
		service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		r := httptest.NewRequest(http.MethodGet, "/?name=report", nil)
		r.SetPathValue("run_id", id)
		w := httptest.NewRecorder()
		service.LiveProgress(w, r)
		if w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE") || len(host.calls) != 1 {
			t.Fatalf("unsafe progress error: %d %s", w.Code, w.Body.String())
		}
	}
}
