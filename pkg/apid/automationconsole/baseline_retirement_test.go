// adr: 1305
package automationconsole

import (
	"html"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBaselineRetirementConfirmationAndRetry(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "success", "conflict", "wrong-receipt"} {
		t.Run(mode, func(t *testing.T) {
			host, form, _ := baselineFixture(t)
			id := uuid.NewString()
			form.Set("retirement_id", id)
			form.Set("baseline_revision", "3")
			if mode != "unconfirmed" {
				form.Set("confirm_retirement", "yes")
			}
			receipt := api.AutomationLiveBaselineRetirement{RetirementID: id, AppID: "app", Name: "report", BaselineName: "production", Revision: 4, RetiredAt: time.Now().UTC()}
			if mode == "wrong-receipt" {
				receipt.Name = "foreign"
			}
			host.responses["baseline-retire"] = inspectionJSON(t, receipt)
			if mode == "conflict" {
				host.responses["baseline-retire"] = Response{Status: 409, Body: []byte(`{"detail":"PRIVATE"}`)}
			}
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			r := reportRequest(form)
			r.SetPathValue("action", "retire-baseline")
			w := httptest.NewRecorder()
			service.Act(w, r)
			calls := 0
			for _, op := range host.calls {
				if op.Kind == "baseline-retire" {
					calls++
					if op.RetirementID != id || op.Body.(api.RetireAutomationLiveBaselineRequest).ExpectedRevision != 3 {
						t.Fatal("changed retirement intent")
					}
				}
				if strings.Contains(op.Kind, "start") || op.Kind == "baseline-promote" {
					t.Fatal("unexpected mutation", op.Kind)
				}
			}
			if mode == "unconfirmed" {
				if calls != 0 || w.Code != 400 {
					t.Fatal("unconfirmed", calls, w.Code)
				}
			} else if calls != 1 {
				t.Fatal("missing retirement")
			}
			if mode == "success" && (w.Code != 200 || !strings.Contains(w.Body.String(), "retired at revision 4")) {
				t.Fatal("success", w.Code, w.Body.String())
			}
			if mode == "conflict" || mode == "wrong-receipt" {
				body := html.UnescapeString(w.Body.String())
				if strings.Contains(body, "PRIVATE") || !strings.Contains(body, id) || !strings.Contains(body, `name="baseline_revision" value="3"`) {
					t.Fatal("lost retry", w.Code, body)
				}
			}
		})
	}
}
