//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRehearsalManagedLocalAppsStartAndStopForBothRevisions(t *testing.T) {
	captureSuiteTestOutput(t)
	o, _ := rehearsalTestRepo(t)
	scenarios, _, err := readTestManifest(o.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	scenario := scenarios[o.Scenario]
	scenario.Local = managedLocalTestSpec(t, "serve")
	scenario.Timeout = "15s"
	scenario.Checks = []testHTTPRequest{{Name: "health", Method: "GET", Path: "/health", Expect: testHTTPExpect{Status: 204}}}
	o.Manifest = writeSuiteTestManifest(t, filepath.Dir(o.Manifest), testManifest{Version: 1, Scenarios: map[string]testScenario{o.Scenario: scenario}})
	o.Engine, o.Repeat = "local", 1
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	report := runTestRehearsal(context.Background(), nil, session, o)
	if report.Status != "failed" {
		t.Fatalf("local behavior change was missed: %+v", report)
	}
	for _, receipt := range append(report.BaselineRuns, report.CandidateRuns...) {
		if receipt.Status != "passed" || receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.Shutdown != "graceful" || receipt.CleanupError != "" {
			t.Fatalf("managed local lifecycle lost: %+v", receipt)
		}
	}
}
