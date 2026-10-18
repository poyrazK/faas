// adr: 1296
package main

import (
	"testing"
)

func TestAutomationLiveSuiteSharesSafeProgressAndResumesOriginalRuns(t *testing.T) {
	for _, mode := range []string{"passed", "lost-ack", "share-fails-before", "share-fails-after", "share-wrong-target"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiveSuiteFixture(t, mode)
			want := 0
			if mode != "passed" {
				want = 3
			}
			result := runLiveSuiteTest(t, append(liveSuiteArgs(f.path), "--share"), want)
			j, err := readAutomationLiveSuite(f.path)
			if err != nil || !j.Plan.Shared {
				t.Fatal("sharing intent was not persisted", err)
			}
			id := sharedAutomationLiveSuiteID(j)
			if !automationLiveUUID(id) {
				t.Fatal("invalid stable shared identity")
			}
			f.mu.Lock()
			starts := len(f.starts)
			f.mu.Unlock()
			if mode == "share-fails-before" || mode == "share-wrong-target" {
				if starts != 0 || result.Summary["not_started"] != 2 {
					t.Fatal("shared metadata failure admitted execution")
				}
			}
			if mode == "share-fails-after" && result.Cases[0].RunID == "" {
				t.Fatal("sharing failure lost an accepted run")
			}
			if mode != "passed" {
				f.mu.Lock()
				f.mode = "passed"
				f.mu.Unlock()
				result = runLiveSuiteTest(t, resumeLiveSuiteArgs(f.path), 0)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.keys) != 2 || result.Summary["passed"] != 2 || f.sharedID != id || len(f.sharedRequest.Cases) != 2 || f.sharedRequest.Cases[0].RunID != result.Cases[0].RunID || f.sharedRequest.Cases[1].RunID != result.Cases[1].RunID {
				t.Fatal("shared history lost or replaced original runs")
			}
			wantStarts := 2
			if mode == "lost-ack" {
				wantStarts = 3
				if f.starts[0] != f.starts[1] {
					t.Fatal("uncertain admission changed its original key or body")
				}
			}
			if len(f.starts) != wantStarts {
				t.Fatal("resume admitted duplicate executions", len(f.starts))
			}
		})
	}
}

func TestAutomationLiveSuiteSharingPreservesCaseIdempotencyKeys(t *testing.T) {
	o, err := parseAutomationLiveSuite(liveSuiteArgs("suite.json")[3:])
	if err != nil {
		t.Fatal(err)
	}
	o.plan.AppID, o.plan.EndpointHash = liveSuiteAppID, liveSuiteHash("endpoint")
	local := liveSuiteCaseKey(o.plan, "first")
	o.plan.Shared = true
	if liveSuiteCaseKey(o.plan, "first") != local {
		t.Fatal("sharing started a different logical case admission")
	}
	if _, err := parseAutomationLiveSuite(append(resumeLiveSuiteArgs("suite.json")[3:], "--share")); err == nil {
		t.Fatal("resume could change journal sharing intent")
	}
}
