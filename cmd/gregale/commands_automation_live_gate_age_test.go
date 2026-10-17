// adr: 1301
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomationGateEvidenceAgeCLI(t *testing.T) {
	for _, mode := range []string{"fresh", "stale", "future", "invalid", "request-error"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			f := authedFakeAPI(t, "", http.StatusOK)
			calls := 0
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Error("mutating request")
				}
				if mode == "request-error" {
					w.WriteHeader(503)
					return
				}
				view := automationGateView()
				view.Candidate.CreatedAt = time.Now().Add(-10 * time.Second)
				if mode == "stale" {
					view.Candidate.CreatedAt = time.Now().Add(-2 * time.Hour)
				}
				if mode == "future" {
					view.Candidate.CreatedAt = time.Now().Add(time.Hour)
					view.Candidate.UpdatedAt = view.Candidate.CreatedAt
				}
				created := view.Candidate.CreatedAt
				view.Cases[0].Candidate.CreatedAt = &created
				_ = json.NewEncoder(w).Encode(view)
			})
			content := `{"version":2,"required_cases":["happy"],"max_candidate_age_seconds":60}`
			if mode == "invalid" {
				content = `{"version":2,"required_cases":["happy"],"max_candidate_age_seconds":0}`
			}
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			code, stderr := runWithStderr(t, func() int { return run(append(automationGateArgs(), "--policy", path)) })
			want := map[string]int{"fresh": 0, "stale": 7, "future": 7, "invalid": 1, "request-error": 3}[mode]
			if code != want {
				t.Fatalf("exit %d: %s %s", code, stderr, out.String())
			}
			if mode == "invalid" {
				if calls != 0 {
					t.Fatal("invalid policy contacted API")
				}
				return
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
			var report automationLiveGateReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.CoveragePolicy == nil || report.PolicyHash != report.CoveragePolicy.Hash() {
				t.Fatal("missing provenance")
			}
			if mode == "request-error" {
				if report.EvidenceAge != nil {
					t.Fatal("invented age on error")
				}
				return
			}
			if report.EvidenceAge == nil || report.EvidenceAge.MaxAgeSeconds != 60 || report.EvidenceAge.EvaluatedAt.IsZero() {
				t.Fatalf("age: %+v", report.EvidenceAge)
			}
		})
	}
}
