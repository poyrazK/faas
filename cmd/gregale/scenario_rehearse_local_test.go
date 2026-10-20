//go:build unix

package main_test

import (
	"context"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestRehearsalManagedLocalAppsStartAndStopForBothRevisions(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	o, _ := main.RehearsalTestRepo(t)
	scenarios, _, err := main.ReadTestManifest(o.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	scenario := scenarios[o.Scenario]
	scenario.Local = managedLocalTestSpec(t, "serve")
	scenario.Timeout = "15s"
	scenario.Checks = []main.TestHTTPRequest{{Name: "health", Method: "GET", Path: "/health", Expect: main.TestHTTPExpect{Status: 204}}}
	o.Manifest = main.WriteSuiteTestManifest(t, filepath.Dir(o.Manifest), main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{o.Scenario: scenario}})
	o.Engine, o.Repeat = "local", 1
	session, err := main.PrepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll((*session.Root())) })
	report := main.RunTestRehearsal(context.Background(), nil, session, o)
	if report.Status != "failed" {
		t.Fatalf("local behavior change was missed: %+v", report)
	}
	for _, receipt := range append(report.BaselineRuns, report.CandidateRuns...) {
		if receipt.Status != "passed" || receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.Shutdown != "graceful" || receipt.CleanupError != "" {
			t.Fatalf("managed local lifecycle lost: %+v", receipt)
		}
	}
}
