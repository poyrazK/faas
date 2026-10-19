// adr: 1302
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

const baselineCLIPromotion = "00000000-0000-4000-8000-000000000020"

func baselineCLIReceipt() api.AutomationLiveBaseline {
	view := automationGateView()
	limit := int64(60)
	policy := automationgate.CoveragePolicy{Version: 2, RequiredCases: []string{"happy"}, MaxCandidateAgeSeconds: &limit}
	raw, _ := json.Marshal(policy)
	return api.AutomationLiveBaseline{PromotionID: baselineCLIPromotion, AppID: liveCLIDeployment, Name: view.Baseline.Name, BaselineName: "production", Revision: 1, ActorAccountID: liveCLIDeployment, PromotedAt: time.Now().UTC(), Suite: view.Baseline, Policy: raw, PolicyHash: policy.Hash(), CasesPassed: 1, OldestRunCreatedAt: time.Now().Add(-10 * time.Second).UTC(), EvidenceAgeSeconds: 10}
}
func namedGateArgs() []string {
	args := automationGateArgs()
	for i, arg := range args {
		if arg == "--baseline" {
			args[i] = "--baseline-name"
			args[i+1] = "production"
			break
		}
	}
	return args
}
func TestAutomationGateNamedBaselineResolvesReviewedPolicy(t *testing.T) {
	for _, mode := range []string{"passed", "stale-candidate", "policy-mismatch", "invalid-receipt", "head-missing", "comparison-missing", "same-candidate"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			fake := authedFakeAPI(t, "", http.StatusOK)
			calls := []string{}
			fake.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.EscapedPath())
				if r.Method != http.MethodGet || r.ContentLength > 0 {
					t.Error("named gate mutated state")
				}
				if len(calls) == 1 {
					if mode == "head-missing" {
						w.WriteHeader(404)
						return
					}
					receipt := baselineCLIReceipt()
					if mode == "invalid-receipt" {
						receipt.PolicyHash = "forged"
					}
					if mode == "same-candidate" {
						receipt.Suite.ID = gateCLICandidate
					}
					_ = json.NewEncoder(w).Encode(receipt)
					return
				}
				if mode == "comparison-missing" {
					w.WriteHeader(404)
					return
				}
				view := automationGateView()
				created := time.Now().Add(-10 * time.Second).UTC()
				if mode == "stale-candidate" {
					created = time.Now().Add(-2 * time.Hour).UTC()
				}
				view.Cases[0].Candidate.CreatedAt = &created
				_ = json.NewEncoder(w).Encode(view)
			})
			args := namedGateArgs()
			if mode == "policy-mismatch" {
				path := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(path, []byte(`{"version":2,"required_cases":["happy"],"max_candidate_age_seconds":120}`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--policy", path)
			}
			code, stderr := runWithStderr(t, func() int { return run(args) })
			want := map[string]int{"passed": 0, "stale-candidate": 7, "policy-mismatch": 7, "invalid-receipt": 3, "head-missing": 4, "comparison-missing": 4, "same-candidate": 1}[mode]
			if code != want {
				t.Fatalf("exit %d, want %d: %s %s", code, want, stderr, out.String())
			}
			var report automationLiveGateReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.BaselineName != "production" {
				t.Fatal("lost alias selection")
			}
			if mode == "passed" || mode == "stale-candidate" || mode == "comparison-missing" {
				if report.ResolvedBaseline == nil || report.ResolvedBaseline.Revision != 1 || report.ResolvedBaseline.PromotionID != baselineCLIPromotion || report.PolicyHash != report.ResolvedBaseline.PolicyHash || report.BaselineID != gateCLIBaseline {
					t.Fatal("lost resolved revision or inherited policy")
				}
				root := "/v1/apps/billing/automations/case%2Freport"
				if !reflect.DeepEqual(calls, []string{"GET " + root + "/live-baselines/production", "GET " + root + "/live-suites/" + gateCLICandidate + "/compare/" + gateCLIBaseline}) {
					t.Fatal("changed read identities", calls)
				}
			} else if len(calls) != 1 {
				t.Fatal("read evidence after invalid baseline", calls)
			}
			if mode == "policy-mismatch" && report.Outcome != "inconclusive" {
				t.Fatal("policy mismatch not inconclusive")
			}
		})
	}
}
func TestAutomationLiveBaselineCLIExplicitWriteAndRead(t *testing.T) {
	for _, verb := range []string{"promote", "show", "history"} {
		t.Run(verb, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			fake := authedFakeAPI(t, "", http.StatusOK)
			calls := 0
			receipt := baselineCLIReceipt()
			fake.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				root := "/v1/apps/billing/automations/case%2Freport/live-baselines/production"
				wantMethod, wantPath := http.MethodGet, root
				if verb == "promote" {
					wantMethod = http.MethodPut
					wantPath += "/promotions/" + baselineCLIPromotion
					var body api.PromoteAutomationLiveBaselineRequest
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedRevision != 0 || body.SuiteID != gateCLIBaseline {
						t.Error("lost promotion fences")
					}
				}
				if verb == "history" {
					wantPath += "/history"
				}
				if r.Method != wantMethod || r.URL.EscapedPath() != wantPath {
					t.Error("wrong baseline request", r.Method, r.URL.EscapedPath())
				}
				if verb == "history" {
					_ = json.NewEncoder(w).Encode(api.AutomationLiveBaselineHistory{Promotions: []api.AutomationLiveBaseline{receipt}})
				} else {
					_ = json.NewEncoder(w).Encode(receipt)
				}
			})
			args := []string{"automations", "live", "baseline", verb, "--app", "billing", "--name", "case/report", "--baseline-name", "production"}
			if verb == "promote" {
				path := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(path, receipt.Policy, 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--suite", gateCLIBaseline, "--promotion-id", baselineCLIPromotion, "--expected-revision", "0", "--expected-version", "7", "--expected-published-version", "4", "--expected-deployment-id", liveCLIDeployment, "--policy", path)
			}
			code, stderr := runWithStderr(t, func() int { return run(args) })
			if code != 0 || calls != 1 || !strings.Contains(out.String(), baselineCLIPromotion) {
				t.Fatalf("exit %d calls %d: %s %s", code, calls, stderr, out.String())
			}
		})
	}
}
func TestAutomationLiveBaselineCLIRejectsAmbiguousSelectionLocally(t *testing.T) {
	for _, args := range [][]string{append(namedGateArgs(), "--baseline", gateCLIBaseline), append(namedGateArgs(), "--confirm-real-actions"), {"automations", "live", "baseline", "promote", "--app", "billing", "--name", "report", "--baseline-name", "production"}} {
		resetJSONOut(t)
		fake := authedFakeAPI(t, "", 200)
		calls := 0
		fake.srv.Config.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })
		code, _ := runWithStderr(t, func() int { return run(args) })
		if code != 1 || calls != 0 {
			t.Fatal("ambiguous or incomplete selection reached API", code, calls)
		}
	}
}
