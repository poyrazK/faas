// adr: 1304
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationLiveBaselineListingCLI(t *testing.T) {
	for _, mode := range []string{"json", "human", "empty", "invalid-health", "duplicate", "foreign", "invalid-receipt"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode != "human"
			out := captureAutomationStdout(t)
			fake := authedFakeAPI(t, "", 200)
			calls := 0
			receipt := baselineCLIReceipt()
			entry := api.AutomationLiveBaselineHealth{Baseline: receipt, EvidenceStatus: "usable", RenewalStatus: "inconclusive", Outdated: true, Reasons: []string{"candidate_outdated"}, Cases: []api.AutomationLiveBaselineCaseHealth{}}
			view := api.ListAutomationLiveBaselinesResponse{ObservedAt: time.Now().UTC(), Baselines: []api.AutomationLiveBaselineHealth{entry}}
			switch mode {
			case "empty":
				view.Baselines = []api.AutomationLiveBaselineHealth{}
			case "invalid-health":
				view.Baselines[0].EvidenceStatus = "healthy-looking-unknown"
			case "duplicate":
				view.Baselines = append(view.Baselines, entry)
			case "foreign":
				view.Baselines[0].Baseline.Name = "foreign"
			case "invalid-receipt":
				view.Baselines[0].Baseline.PolicyHash = "invalid"
			}
			fake.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-baselines" || r.ContentLength > 0 {
					t.Error("list performed another operation")
				}
				_ = json.NewEncoder(w).Encode(view)
			})
			code, _ := runWithStderr(t, func() int {
				return run([]string{"automations", "live", "baseline", "list", "--app", "billing", "--name", "case/report"})
			})
			want := 0
			if strings.HasPrefix(mode, "invalid-") || mode == "duplicate" || mode == "foreign" {
				want = 3
			}
			if code != want || calls != 1 {
				t.Fatalf("exit %d want %d calls %d", code, want, calls)
			}
			if mode == "human" && (!strings.Contains(out.String(), "historical targets") || !strings.Contains(out.String(), "evidence usable")) {
				t.Fatal("misleading health", out.String())
			}
		})
	}
	if _, err := parseAutomationLiveBaseline([]string{"list", "--app", "billing", "--name", "report", "--baseline-name", "production"}); err == nil {
		t.Fatal("listing accepted unused selector")
	}
}
