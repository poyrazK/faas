// adr: 1297
package automationconsole

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedSuiteProgressRequiresExplicitNarrowPost(t *testing.T) {
	for _, mode := range []string{"valid", "missing-confirmation", "unknown", "oversized", "wrong-id", "wrong-name", "unmanaged", "backend-error"} {
		t.Run(mode, func(t *testing.T) {
			host, id := inspectionFixture(t)
			view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: "report", Managed: true}, Outcome: "pending"}
			form := url.Values{"csrf_token": {"test-csrf"}, "confirm_real_actions": {"yes"}}
			want := 200
			switch mode {
			case "missing-confirmation":
				form.Del("confirm_real_actions")
				want = 400
			case "unknown":
				form.Set("run_id", "PRIVATE-run")
				want = 400
			case "oversized":
				form.Set("csrf_token", strings.Repeat("x", int(api.AutomationLiveProgressFormMaxBytes)+1))
				want = 400
			case "wrong-id":
				view.ID = uuid.NewString()
				want = 503
			case "wrong-name":
				view.Name = "PRIVATE-foreign"
				want = 503
			case "unmanaged":
				view.Managed = false
				want = 503
			case "backend-error":
				want = 503
			}
			host.responses["live-suite-advance"] = inspectionJSON(t, view)
			if mode == "backend-error" {
				host.responses["live-suite-advance"] = Response{Status: 503, Body: []byte(`{"detail":"PRIVATE-error"}`)}
			}
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			r := reportRequest(form)
			r.URL.RawQuery = "name=report"
			r.SetPathValue("suite_id", id)
			w := httptest.NewRecorder()
			service.AdvanceLiveSuite(w, r)
			if w.Code != want || strings.Contains(w.Body.String(), "PRIVATE") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsafe response %d %s", w.Code, w.Body.String())
			}
			if want == 400 {
				if len(host.calls) != 0 {
					t.Fatal("invalid request reached backend")
				}
			} else {
				if len(host.calls) != 1 || host.calls[0].Kind != "live-suite-advance" || host.calls[0].RunID != id {
					t.Fatal("continuation changed scope or executed extra actions")
				}
				body, ok := host.calls[0].Body.(api.AdvanceAutomationLiveSuiteRequest)
				if !ok || !body.ConfirmRealActions {
					t.Fatal("missing explicit authorization")
				}
			}
		})
	}
}

func TestManagedSuiteHistoryNeverEnablesAutomaticExecution(t *testing.T) {
	host, id := inspectionFixture(t)
	view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: "report", Managed: true}, Outcome: "pending"}
	host.responses["live-suite-list"] = inspectionJSON(t, api.ListAutomationLiveSuitesResponse{})
	host.responses["live-suite-get"] = inspectionJSON(t, view)
	service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	service.Show(w, httptest.NewRequest(http.MethodGet, "/?name=report&suite="+id+"&auto=true", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-auto="false"`) {
		t.Fatal("browsing enabled real-action advancement")
	}
	for _, call := range host.calls {
		if call.Kind == "live-suite-start" || call.Kind == "live-suite-advance" || call.Kind == "live-check" {
			t.Fatal("history read mutated execution")
		}
	}
}
