// adr: 1300
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomationGateCoverageCLI(t *testing.T) {
	for _, mode := range []string{"covered", "missing", "invalid", "request-error"} {
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
				_ = json.NewEncoder(w).Encode(automationGateView())
			})
			content := `{"version":1,"required_cases":["happy"]}`
			if mode == "missing" {
				content = `{"version":1,"required_cases":["critical"]}`
			}
			if mode == "invalid" {
				content = `{"version":1,"required_cases":["PRIVATE","PRIVATE"]}`
			}
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			args := append(automationGateArgs(), "--policy", path)
			code, stderr := runWithStderr(t, func() int { return run(args) })
			want := map[string]int{"covered": 0, "missing": 7, "invalid": 1, "request-error": 3}[mode]
			if code != want {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if mode == "invalid" {
				if calls != 0 || strings.Contains(stderr, "PRIVATE") {
					t.Fatal("invalid policy was sent or exposed")
				}
				return
			}
			if calls != 1 {
				t.Fatalf("requests: %d", calls)
			}
			var report automationLiveGateReport
			if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
				t.Fatal(err)
			}
			if report.CoveragePolicy == nil || report.PolicyHash != report.CoveragePolicy.Hash() {
				t.Fatal("missing policy provenance")
			}
			if mode == "missing" && strings.Join(report.MissingRequiredCases, ",") != "critical" {
				t.Fatalf("missing cases: %v", report.MissingRequiredCases)
			}
		})
	}
}
