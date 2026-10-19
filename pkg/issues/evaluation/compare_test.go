// adr: 1661
package evaluation

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func testCases() []Case {
	return []Case{
		{ID: "a", Family: "one", Event: api.IssueEvent{ExceptionType: "Error", ErrorCode: "SAME", Message: "same"}},
		{ID: "b", Family: "one", Event: api.IssueEvent{ExceptionType: "Error", ErrorCode: "SAME", Message: "same"}},
		{ID: "c", Family: "two", Event: api.IssueEvent{ExceptionType: "Error", ErrorCode: "OTHER", Message: "same"}},
	}
}

func TestComparisonExplainsChangesWithoutEventValues(t *testing.T) {
	cases := testCases()
	for i := range cases {
		cases[i].Family += "-private-family"
		cases[i].App = "private-app"
		cases[i].Environment = "private-environment"
		cases[i].Event.ExceptionType = "PrivateExceptionType"
		cases[i].Event.Message = "private.person@example.com"
		cases[i].Event.Frames = []api.IssueFrame{
			{File: "/app/private-file.js", Function: "privateFunction", InApp: true},
			{File: "/app/caller.js", Function: fmt.Sprint(i), InApp: true},
		}
	}
	now := time.Now().UTC()
	baseline, err := Snapshot(cases, "legacy", now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(cases, baseline, "failure-site-v1", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Regressed || report.MergedPairs != 1 || report.FixedFalseSplitPairs != 1 || len(report.Changes) != 2 {
		t.Fatalf("unexpected comparison: %+v", report)
	}
	for _, c := range report.Changes {
		if c.Change != "merged" || c.BaselineDecision.Basis != "application-frames" || c.CandidateDecision.Basis != "failure-site" || c.CandidateDecision.DiscriminatorKind != "error-code" || !slices.Equal(c.CandidateDecision.FrameIndexes, []int{0}) {
			t.Fatalf("wrong selected evidence: %+v", c)
		}
	}
	raw, err := json.Marshal([]any{baseline, report})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"private-family", "private-app", "private-environment", "PrivateExceptionType", "private.person", "private-file", "privateFunction", "SAME", "OTHER", "fingerprint", "exception_type"} {
		if strings.Contains(string(raw), value) {
			t.Fatalf("report leaked event or label evidence %q", value)
		}
	}
}

func TestEquivalentPartitionsAndReorderingAreStable(t *testing.T) {
	cases := testCases()
	for i := range cases {
		cases[i].Event.FingerprintOverride = "same-override"
	}
	now := time.Now().UTC()
	before, err := Snapshot(cases, "legacy", now)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(cases)
	after, err := Snapshot(cases, "legacy", now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot depends on order/time: %v", err)
	}
	report, err := Compare(cases, before, "cause-chain-v1", now)
	if err != nil || len(report.Changes) != 0 || report.MergedPairs != 0 || report.SplitPairs != 0 || report.Regressed {
		t.Fatalf("version-only change altered membership: %+v %v", report, err)
	}
	slices.Reverse(before.Assignments)
	reordered, err := Compare(cases, before, "cause-chain-v1", now)
	if err != nil || !reflect.DeepEqual(reordered, report) {
		t.Fatalf("baseline assignment order changed report: %v", err)
	}
}

func TestSnapshotPreservesInputEvidence(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := DecodeCorpus(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	for _, strategy := range []string{"legacy", "failure-site-v1", "cause-chain-v1"} {
		if _, err := Snapshot(cases, strategy, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := json.Marshal(cases)
	if err != nil || string(before) != string(after) {
		t.Fatalf("evaluation mutated input evidence: %v", err)
	}
}

func TestNewIncorrectPairsFailDespiteEqualAggregateErrors(t *testing.T) {
	for _, family := range []string{"same", "different"} {
		t.Run(family, func(t *testing.T) {
			cases := testCases()
			for i := range cases {
				cases[i].Family = family
				if family == "different" {
					cases[i].Family = fmt.Sprint(i)
				}
			}
			now := time.Now().UTC()
			baseline, err := Snapshot(cases, "failure-site-v1", now)
			if err != nil {
				t.Fatal(err)
			}
			// Historical partition: {a,c}, {b}; candidate: {a,b}, {c}.
			baseline.Assignments[1].Group = "b"
			baseline.Assignments[2].Group = "a"
			report, err := Compare(cases, baseline, "failure-site-v1", now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Baseline.FalseMergePairs != report.Candidate.FalseMergePairs || report.Baseline.FalseSplitPairs != report.Candidate.FalseSplitPairs || !report.Regressed {
				t.Fatalf("equal totals hid regression: %+v", report)
			}
			if family == "same" && (report.NewFalseSplitPairs != 1 || report.FixedFalseSplitPairs != 1) {
				t.Fatalf("missed exchanged split error: %+v", report)
			}
			if family == "different" && (report.NewFalseMergePairs != 1 || report.FixedFalseMergePairs != 1) {
				t.Fatalf("missed exchanged merge error: %+v", report)
			}
		})
	}
}

func TestComparisonMatchesQuadraticOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 73))
	for trial := 0; trial < 200; trial++ {
		var cases []Case
		var old, candidate []int
		for i := 0; i < 24; i++ {
			cases = append(cases, Case{ID: fmt.Sprintf("case-%02d", i), Family: fmt.Sprint(rng.IntN(4)), App: fmt.Sprint(i % 2)})
			old = append(old, rng.IntN(5))
			candidate = append(candidate, rng.IntN(5))
		}
		left, right := partition(cases, old), partition(cases, candidate)
		got := comparePartitions(cases, left, right)
		var merged, split, newMerge, fixedMerge, newSplit, fixedSplit int64
		var changedIDs []string
		for i, a := range cases {
			changed := false
			for j, b := range cases {
				before := a.App == b.App && old[i] == old[j]
				after := a.App == b.App && candidate[i] == candidate[j]
				changed = changed || before != after
				if j <= i || before == after {
					continue
				}
				sameFamily := a.App == b.App && a.Family == b.Family
				if after {
					merged++
					if sameFamily {
						fixedSplit++
					} else {
						newMerge++
					}
				} else {
					split++
					if sameFamily {
						newSplit++
					} else {
						fixedMerge++
					}
				}
			}
			if changed {
				changedIDs = append(changedIDs, a.ID)
			}
		}
		if got.MergedPairs != merged || got.SplitPairs != split || got.NewFalseMergePairs != newMerge || got.FixedFalseMergePairs != fixedMerge || got.NewFalseSplitPairs != newSplit || got.FixedFalseSplitPairs != fixedSplit || got.Regressed != (newMerge > 0 || newSplit > 0) {
			t.Fatalf("trial %d differs from oracle: %+v want %v", trial, got, []int64{merged, split, newMerge, fixedMerge, newSplit, fixedSplit})
		}
		var gotIDs []string
		for _, c := range got.Changes {
			gotIDs = append(gotIDs, c.ID)
		}
		if !slices.Equal(gotIDs, changedIDs) {
			t.Fatalf("changed membership differs: %v != %v", gotIDs, changedIDs)
		}
	}
}

func partition(cases []Case, groups []int) Baseline {
	minimum := map[string]string{}
	var baseline Baseline
	for i, c := range cases {
		group := key(c.App, fmt.Sprint(groups[i]))
		if _, ok := minimum[group]; !ok {
			minimum[group] = c.ID
		}
		baseline.Assignments = append(baseline.Assignments, Assignment{ID: c.ID, Group: minimum[group]})
	}
	return baseline
}

func TestMaximumCorpusProducesLinearSizePreview(t *testing.T) {
	cases := make([]Case, MaxCases)
	old, candidate := make([]int, MaxCases), make([]int, MaxCases)
	for i := range cases {
		cases[i] = Case{ID: fmt.Sprintf("c-%05d", i), Family: fmt.Sprint(i)}
		old[i] = i
	}
	report := comparePartitions(cases, partition(cases, old), partition(cases, candidate))
	if report.NewFalseMergePairs != pairs(MaxCases) || len(report.Changes) != MaxCases {
		t.Fatalf("lost bounded preview or exact counts: %d %d", report.NewFalseMergePairs, len(report.Changes))
	}
}

func TestBaselineValidationAndCorpusBinding(t *testing.T) {
	cases := testCases()
	cases[2].App = "other"
	now := time.Now().UTC()
	original, err := Snapshot(cases, "cause-chain-v1", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*Baseline)
	}{
		{"version", func(b *Baseline) { b.Version = 2 }},
		{"strategy", func(b *Baseline) { b.Strategy = "future" }},
		{"digest", func(b *Baseline) { b.CorpusSHA256 = "wrong" }},
		{"missing", func(b *Baseline) { b.Assignments = b.Assignments[:2] }},
		{"duplicate", func(b *Baseline) { b.Assignments[1].ID = "a" }},
		{"unknown", func(b *Baseline) { b.Assignments[1].ID = "unknown" }},
		{"group-name", func(b *Baseline) { b.Assignments[1].Group = "absent" }},
		{"namespace", func(b *Baseline) { b.Assignments[2].Group = "a" }},
		{"source-kind", func(b *Baseline) { b.Assignments[0].Decision.SourceKind = "private-value" }},
		{"basis", func(b *Baseline) { b.Assignments[0].Decision.Basis = "private-value" }},
		{"discriminator", func(b *Baseline) { b.Assignments[0].Decision.DiscriminatorKind = "private-value" }},
		{"frame-range", func(b *Baseline) { b.Assignments[0].Decision.FrameIndexes = []int{api.IssueMaxFrames} }},
		{"cause-range", func(b *Baseline) { n := api.IssueMaxCauses; b.Assignments[0].Decision.CauseIndex = &n }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			b, err := DecodeBaseline(raw)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&b)
			if _, err := Compare(cases, b, "cause-chain-v1", now); err == nil {
				t.Fatal("invalid baseline accepted")
			}
		})
	}
	for _, mutate := range []func([]Case){
		func(c []Case) { c[0].Family = "changed" },
		func(c []Case) { c[0].Event.Message = "changed" },
		func(c []Case) { c[0].Environment = "changed" },
	} {
		changed := slices.Clone(cases)
		mutate(changed)
		if _, err := Compare(changed, original, "cause-chain-v1", now); err == nil {
			t.Fatal("changed corpus reused a reviewed baseline")
		}
	}
}

func TestReviewedSyntheticBaseline(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := DecodeCorpus(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, strategy := range []string{"legacy", "failure-site-v1", "cause-chain-v1"} {
		fresh, err := Snapshot(cases, strategy, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		report, err := Compare(cases, fresh, strategy, time.Now().UTC())
		if err != nil || report.Regressed || len(report.Changes) != 0 {
			t.Fatalf("strategy snapshot cannot validate itself: %s %+v %v", strategy, report, err)
		}
	}
	raw, err = os.ReadFile("testdata/cause-chain-v1.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := DecodeBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Compare(cases, baseline, "cause-chain-v1", time.Now().UTC())
	if err != nil || report.Regressed {
		t.Fatalf("reviewed grouping baseline regressed; inspect with make issues-grouping-check: %+v %v", report, err)
	}
}
