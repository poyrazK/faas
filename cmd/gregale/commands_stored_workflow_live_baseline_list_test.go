// adr: 1304
package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStoredWorkflowLiveBaselineListingCLI(t *testing.T) {
	for _, mode := range []string{"json", "human", "empty", "invalid-health", "duplicate", "foreign", "invalid-receipt"} {
		t.Run(mode, func(t *testing.T) {
			main.ResetJSONOut(t)
			(*main.JsonOutput) = mode != "human"
			out := main.CaptureStoredWorkflowStdout(t)
			fake := main.AuthedFakeAPI(t, "", 200)
			calls := 0
			receipt := baselineCLIReceipt()
			entry := api.StoredWorkflowLiveBaselineHealth{Baseline: receipt, EvidenceStatus: "usable", RenewalStatus: "inconclusive", Outdated: true, Reasons: []string{"candidate_outdated"}, Cases: []api.StoredWorkflowLiveBaselineCaseHealth{}}
			view := api.ListStoredWorkflowLiveBaselinesResponse{ObservedAt: time.Now().UTC(), Baselines: []api.StoredWorkflowLiveBaselineHealth{entry}}
			switch mode {
			case "empty":
				view.Baselines = []api.StoredWorkflowLiveBaselineHealth{}
			case "invalid-health":
				view.Baselines[0].EvidenceStatus = "healthy-looking-unknown"
			case "duplicate":
				view.Baselines = append(view.Baselines, entry)
			case "foreign":
				view.Baselines[0].Baseline.Name = "foreign"
			case "invalid-receipt":
				view.Baselines[0].Baseline.PolicyHash = "invalid"
			}
			(*fake.Srv()).Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.EscapedPath() != "/v1/apps/billing/workflows/stored/case%2Freport/live-baselines" || r.ContentLength > 0 {
					t.Error("list performed another operation")
				}
				_ = json.NewEncoder(w).Encode(view)
			})
			code, _ := main.RunWithStderr(t, func() int {
				return main.Run([]string{"stored-workflows", "live", "baseline", "list", "--app", "billing", "--name", "case/report"})
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
	if _, err := main.ParseStoredWorkflowLiveBaseline([]string{"list", "--app", "billing", "--name", "report", "--baseline-name", "production"}); err == nil {
		t.Fatal("listing accepted unused selector")
	}
}
