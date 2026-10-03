//go:build unix

package main

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenarioSuiteManagedAppsFinishShutdownBeforeNextMember(t *testing.T) {
	for _, failFast := range []bool{false, true} {
		name := "complete"
		if failFast {
			name = "fail-fast"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr := captureSuiteTestOutput(t)
			first, second := suiteTestScenario("/health"), suiteTestScenario("/health")
			for _, scenario := range []*testScenario{&first, &second} {
				scenario.Local = managedLocalTestSpec(t, "serve")
				scenario.Checks[0].Expect.Status = 204
				scenario.Cleanup = [][]string{managedLocalFixtureCommand(t, "cleanup")}
			}
			if failFast {
				first.Checks[0].Expect.Status = 418
			}
			manifest := writeSuiteTestManifest(t, dir, testManifest{
				Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
				Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "first", Engine: "local"}, {Scenario: "second", Engine: "local"}}}},
			})
			args := []string{"--suite", "smoke", "--manifest", manifest}
			wantExit, wantStarts := 0, 2
			if failFast {
				args = append(args, "--fail-fast")
				wantExit, wantStarts = 1, 1
			}
			if code := cmdTest(args); code != wantExit {
				t.Fatalf("managed suite exit %d: %s", code, stderr)
			}
			var receipts []testRunReceipt
			if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 {
				t.Fatalf("managed suite receipts = %s (%v)", stdout, err)
			}
			for _, receipt := range receipts[:wantStarts] {
				if receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.Shutdown != "graceful" || receipt.CleanupError != "" {
					t.Fatalf("app was not cleaned up: %+v", receipt)
				}
			}
			if failFast && (receipts[1].Status != "skipped" || receipts[1].LocalApp != nil) {
				t.Fatalf("skipped member started an app: %+v", receipts[1])
			}
			body, err := os.ReadFile(filepath.Join(dir, "starts.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			starts := strings.Split(strings.TrimSpace(string(body)), "\n")
			if len(starts) != wantStarts {
				t.Fatalf("app starts = %s", body)
			}
			seenRuns := map[string]bool{}
			for _, start := range starts {
				var marker map[string]string
				if err := json.Unmarshal([]byte(start), &marker); err != nil {
					t.Fatal(err)
				}
				if marker["run"] == "" || seenRuns[marker["run"]] {
					t.Fatalf("member reused managed app identity: %v", marker)
				}
				seenRuns[marker["run"]] = true
				endpoint, err := url.Parse(marker["url"])
				if err != nil {
					t.Fatal(err)
				}
				if conn, err := net.DialTimeout("tcp", endpoint.Host, 100*time.Millisecond); err == nil {
					_ = conn.Close()
					t.Fatalf("managed suite leaked listening app %s", endpoint.Host)
				}
			}
			cleanup, err := os.ReadFile(filepath.Join(dir, "order.log"))
			if err != nil || strings.Count(string(cleanup), "cleanup:\n") != wantStarts {
				t.Fatalf("cleanup did not reach running apps: %s (%v)", cleanup, err)
			}
		})
	}
}
