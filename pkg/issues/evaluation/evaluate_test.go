package evaluation

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPairwiseLabelsExposeFalseMergesAndSplits(t *testing.T) {
	frame := api.IssueFrame{File: "/app/payment.go", Function: "charge", InApp: true}
	base := api.IssueEvent{ExceptionType: "Error", Message: "payment failed", Frames: []api.IssueFrame{frame}}
	second := base
	second.Frames = append(append([]api.IssueFrame(nil), base.Frames...), api.IssueFrame{File: "/app/server.go", Function: "handler", InApp: true})
	cases := []Case{{ID: "one", Family: "declined", Event: base}, {ID: "two", Family: "declined", Event: second}, {ID: "three", Family: "expired", Event: base}}
	report, err := Evaluate(cases, "legacy", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if report.FalseMergePairs != 1 || report.FalseSplitPairs != 1 || report.Groups != 2 || report.Pairs != 3 {
		t.Fatalf("wrong pair counts: %+v", report)
	}
	cases[2].App = "other-app"
	report, err = Evaluate(cases, "legacy", time.Now().UTC())
	if err != nil || report.FalseMergePairs != 0 {
		t.Fatal("cross-app grouping contaminated the evaluation")
	}
	cases[1].ID = cases[0].ID
	if _, err = Evaluate(cases, "legacy", time.Now().UTC()); err == nil {
		t.Fatal("duplicate corpus IDs accepted")
	}
}
func TestCuratedCorpusShowsFailureSiteAndCauseGroupingTradeoffs(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []Case
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	legacy, err := Evaluate(cases, "legacy", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	failure, err := Evaluate(cases, "failure-site-v1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	cause, err := Evaluate(cases, "cause-chain-v1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if failure.FalseMergePairs >= legacy.FalseMergePairs || failure.FalseSplitPairs >= legacy.FalseSplitPairs {
		t.Fatalf("failure-site corpus regressed: legacy=%+v failure=%+v", legacy, failure)
	}
	if cause.FalseSplitPairs >= failure.FalseSplitPairs {
		t.Fatalf("cause-aware corpus did not reduce wrapper fragmentation: failure=%+v cause=%+v", failure, cause)
	}
	if cause.FalseMergePairs > failure.FalseMergePairs {
		t.Fatalf("cause-aware corpus introduced false merges: failure=%+v cause=%+v", failure, cause)
	}
}
