package profiling

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func regressionProfile(seconds, cpu float64) api.ProfileResponse {
	end := time.Now().UTC()
	return api.ProfileResponse{Query: api.ProfileQuery{Runtime: "node24", Start: end.Add(-time.Duration(seconds * float64(time.Second))), End: end}, CPUSeconds: cpu,
		Functions:  []api.ProfileFunction{{Name: "work", File: "app.js", SelfCPUSeconds: cpu}},
		Flamegraph: &api.ProfileStack{Name: "all", CPUSeconds: cpu, Children: []*api.ProfileStack{{Name: "work", File: "app.js", CPUSeconds: cpu}}},
		Coverage:   &api.ProfileCoverage{Available: true, ReceivedProfiles: 10, ContributingCollectors: 1, WindowSeconds: seconds, CoveredSeconds: seconds}}
}

func TestRegressionAssessmentCoverageAndThresholds(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		change       func(*api.ProfileResponse, *api.ProfileResponse, *api.ProfileRegressionOptions)
	}{
		{"increase", "regressed", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { *b = regressionProfile(100, 40) }},
		{"inclusive threshold boundary", "regressed", func(_, b *api.ProfileResponse, o *api.ProfileRegressionOptions) {
			*b = regressionProfile(100, 24)
			o.AbsoluteIncreaseCPUPerSecond = .04
		}},
		{"different window lengths", "no_regression_detected", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { *b = regressionProfile(200, 40) }},
		{"absolute floor", "no_regression_detected", func(_, b *api.ProfileResponse, o *api.ProfileRegressionOptions) {
			*b = regressionProfile(100, 40)
			o.AbsoluteIncreaseCPUPerSecond = .3
		}},
		{"relative floor", "no_regression_detected", func(_, b *api.ProfileResponse, o *api.ProfileRegressionOptions) {
			*b = regressionProfile(100, 40)
			o.RelativeIncreasePercent = 110
		}},
		{"missing coverage", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Coverage = nil }},
		{"sparse capture", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Coverage.CoveredSeconds = 79 }},
		{"too few profiles", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Coverage.ReceivedProfiles = 2 }},
		{"failed upload", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Coverage.RecordedFailedUploads = 1 }},
		{"wrong coverage window", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Coverage.WindowSeconds = 10 }},
		{"empty is unknown", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Empty = true }},
		{"zero baseline", "inconclusive", func(a, _ *api.ProfileResponse, _ *api.ProfileRegressionOptions) { *a = regressionProfile(100, 0) }},
		{"one sided symbols", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) {
			b.Functions[0].Name = "new"
			b.Flamegraph.Children[0].Name = "new"
		}},
		{"missing paths", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.Flamegraph = nil }},
		{"invalid CPU", "inconclusive", func(_, b *api.ProfileResponse, _ *api.ProfileRegressionOptions) { b.CPUSeconds = math.NaN() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, options := regressionProfile(100, 20), regressionProfile(100, 20), api.DefaultProfileRegressionOptions()
			tc.change(&a, &b, &options)
			out := AssessRegression(NewRegressionAssessment(api.ProfileInvestigation{Revision: 3}, options, time.Now()), a, b)
			if out.Status != tc.status || out.Reason == "" || out.InvestigationRevision != 4 {
				t.Fatalf("%+v", out)
			}
			if out.Status == "regressed" && (len(out.Evidence) != 2 || !out.Total.ExceedsThreshold) {
				t.Fatal("missing evidence", out)
			}
		})
	}
}

func TestRegressionRedistributionAndCompleteCallerPaths(t *testing.T) {
	a, b := regressionProfile(100, 40), regressionProfile(100, 40)
	a.Functions = []api.ProfileFunction{{Name: "work", File: "app.js", SelfCPUSeconds: 10}, {Name: "cache", SelfCPUSeconds: 30}}
	b.Functions = []api.ProfileFunction{{Name: "work", File: "app.js", SelfCPUSeconds: 30}, {Name: "cache", SelfCPUSeconds: 10}}
	for _, p := range []*api.ProfileResponse{&a, &b} {
		p.Flamegraph.Children = []*api.ProfileStack{{Name: "callerA", CPUSeconds: 30, Children: []*api.ProfileStack{{Name: "work", File: "app.js", CPUSeconds: 20}}}, {Name: "callerB", CPUSeconds: 10, Children: []*api.ProfileStack{{Name: "work", File: "app.js", CPUSeconds: 10}}}}
	}
	b.Flamegraph.Children[0].Children[0].CPUSeconds = 30
	b.Flamegraph.Children[0].Children[0].Line = 42 // moved named function remains comparable
	out := AssessRegression(NewRegressionAssessment(api.ProfileInvestigation{}, api.DefaultProfileRegressionOptions(), time.Now()), a, b)
	if out.Status != "regressed" || out.Total.ExceedsThreshold {
		t.Fatal(out)
	}
	paths := 0
	for _, e := range out.Evidence {
		if e.Kind == "call_path" {
			paths++
			if len(e.Frames) != 3 || e.Frames[1].Name != "callerA" || e.Frames[2].Line != 42 {
				t.Fatal("caller identity lost", e)
			}
		}
	}
	if paths != 1 {
		t.Fatal("unrelated caller was marked regressed", out)
	}
}

func TestRegressionConfigurationAndEvidenceBounds(t *testing.T) {
	for _, edit := range []func(*api.ProfileRegressionOptions){
		func(o *api.ProfileRegressionOptions) { o.MinimumCoverageRatio = 0 },
		func(o *api.ProfileRegressionOptions) { o.MinimumProfiles = 0 },
		func(o *api.ProfileRegressionOptions) { o.AbsoluteIncreaseCPUPerSecond = 0 },
		func(o *api.ProfileRegressionOptions) { o.RelativeIncreasePercent = math.Inf(1) },
	} {
		o := api.DefaultProfileRegressionOptions()
		edit(&o)
		if ValidateRegressionOptions(o) == nil {
			t.Fatal("unsafe configuration accepted", o)
		}
	}
	entries := []api.ProfileRegressionEvidence{}
	for i := range 100 {
		entries = append(entries, api.ProfileRegressionEvidence{Kind: "function", Frames: []api.ProfileCallPathFrame{{Name: strings.Repeat("é", 2048)}}, Metric: api.ProfileRegressionMetric{DeltaCPUPerSecond: float64(i)}})
	}
	out := boundedRegressionEvidence(entries)
	body, err := json.Marshal(out)
	if err != nil || len(out) > api.ProfileRegressionMaxEvidence || len(body) > api.ProfileRegressionMaxEvidenceBytes+api.ProfileRegressionMaxEvidence+2 || out[0].Metric.DeltaCPUPerSecond != 99 {
		t.Fatal("evidence bound or ordering", len(body), err)
	}
}
