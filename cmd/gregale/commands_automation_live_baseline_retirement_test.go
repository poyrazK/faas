// adr: 1305
package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationLiveBaselineRetirementCLI(t *testing.T) {
	for _, mode := range []string{"json", "human", "wrong-revision", "wrong-id", "retired"} {
		t.Run(mode, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = mode != "human"
			out := main.CaptureAutomationStdout(t)
			fake := main.AuthedFakeAPI(t, "", 200)
			calls := 0
			id := uuid.NewString()
			receipt := api.AutomationLiveBaselineRetirement{RetirementID: id, AppID: uuid.NewString(), Name: "case/report", BaselineName: "production", Revision: 4, PreviousPromotionID: uuid.NewString(), ActorAccountID: uuid.NewString(), RetiredAt: time.Now().UTC()}
			if mode == "wrong-revision" {
				receipt.Revision = 5
			}
			if mode == "wrong-id" {
				receipt.RetirementID = uuid.NewString()
			}
			(*fake.Srv()).Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-baselines/production/retirements/"+id {
					t.Error("retirement route")
				}
				var request api.RetireAutomationLiveBaselineRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedRevision != 3 {
					t.Error("retirement revision")
				}
				if mode == "retired" {
					api.WriteProblem(w, api.NewProblem(410, api.CodeConflict, "Baseline retired", "permanently retired"))
					return
				}
				_ = json.NewEncoder(w).Encode(receipt)
			})
			code, _ := main.RunWithStderr(t, func() int {
				return main.Run([]string{"automations", "live", "baseline", "retire", "--app", "billing", "--name", "case/report", "--baseline-name", "production", "--retirement-id", id, "--expected-revision", "3", "--confirm-retirement"})
			})
			if calls != 1 || ((mode == "json" || mode == "human") && code != 0) || ((strings.HasPrefix(mode, "wrong-") || mode == "retired") && code == 0) {
				t.Fatal("retirement exit", code, calls)
			}
			if mode == "human" && !strings.Contains(out.String(), "retired permanently") {
				t.Fatal(out.String())
			}
		})
	}
	for _, extra := range [][]string{nil, {"--retirement-id", uuid.NewString()}, {"--retirement-id", uuid.NewString(), "--expected-revision", "0", "--confirm-retirement"}, {"--retirement-id", uuid.NewString(), "--expected-revision", "2"}} {
		args := append([]string{"retire", "--app", "billing", "--name", "report", "--baseline-name", "production"}, extra...)
		if _, err := main.ParseAutomationLiveBaseline(args); err == nil {
			t.Fatal("unconfirmed retirement accepted")
		}
	}
}

func TestAutomationGateRetiredBaselineNeverFallsBack(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	out := main.CaptureAutomationStdout(t)
	fake := main.AuthedFakeAPI(t, "", 200)
	calls := 0
	(*fake.Srv()).Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/live-baselines/production") {
			t.Error("unexpected fallback", r.Method, r.URL.Path)
		}
		api.WriteProblem(w, api.NewProblem(410, api.CodeConflict, "Baseline retired", "This baseline has been permanently retired. Select an active baseline explicitly."))
	})
	code, stderr := main.RunWithStderr(t, func() int { return main.Run(namedGateArgs()) })
	if code != 4 || calls != 1 || !strings.Contains(out.String()+stderr, "baseline_retired") {
		t.Fatal("retired gate", code, calls, stderr, out.String())
	}
}

func TestAutomationLiveBaselineRetirementHelpIsLocal(t *testing.T) {
	for _, args := range [][]string{{"automations", "live", "baseline", "retire", "--help"}, {"help", "automations", "live", "baseline", "retire"}, {"automations", "live", "baseline", "retire", "--expected-revision", "3", "--help"}, {"automations", "live", "baseline", "--help"}} {
		main.ResetJSONOut(t)
		out := main.CaptureAutomationStdout(t)
		fake := main.AuthedFakeAPI(t, "unexpected API request", 500)
		calls := 0
		(*fake.Srv()).Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
		code, _ := main.RunWithStderr(t, func() int { return main.Run(args) })
		if code != 0 || calls != 0 || !strings.Contains(out.String(), "retire") {
			t.Fatal("help performed work", code, calls, out.String())
		}
		if strings.Contains(strings.Join(args, " "), "baseline retire") && (!strings.Contains(out.String(), "--confirm-retirement") || !strings.Contains(out.String(), "--retirement-id")) {
			t.Fatal("missing retirement help", out.String())
		}
	}
}
