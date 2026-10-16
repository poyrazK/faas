package ci

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBackendObservationRejectsRegressionsAndUnboundedOutput(t *testing.T) {
	plan, _, err := Compile(executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	previous := Attempt{Steps: []StepResult{{Name: "prepare", Status: StatusQueued}, {Name: "unit", Status: StatusQueued}}}
	valid := Observation{Status: StatusSucceeded, ExitCode: intPtr(0), Steps: []StepResult{
		{Name: "prepare", Status: StatusSucceeded, ExitCode: intPtr(0)}, {Name: "unit", Status: StatusSucceeded, ExitCode: intPtr(0)}},
		Artifacts: []ArtifactRef{{Path: "reports/deep/unit.xml", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 1}}}
	if err := validateObservation(plan.Jobs[0], previous, valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Attempt, *Observation)
	}{
		{"unknown-state", func(_ *Attempt, o *Observation) { o.Status = "invented" }},
		{"step-count", func(_ *Attempt, o *Observation) { o.Steps = o.Steps[:1] }},
		{"step-name", func(_ *Attempt, o *Observation) { o.Steps[0].Name = "replacement" }},
		{"step-order", func(_ *Attempt, o *Observation) { o.Steps[0] = StepResult{Name: "prepare", Status: StatusQueued} }},
		{"failure-with-zero-exit", func(_ *Attempt, o *Observation) { o.Status = StatusFailed }},
		{"success-without-exit", func(_ *Attempt, o *Observation) { o.ExitCode = nil }},
		{"step-exit-code", func(_ *Attempt, o *Observation) { o.Steps[0].ExitCode = intPtr(7) }},
		{"terminal-step-rewritten", func(a *Attempt, o *Observation) {
			a.Steps[0] = StepResult{Name: "prepare", Status: StatusFailed, ExitCode: intPtr(1)}
		}},
		{"running-regressed", func(a *Attempt, o *Observation) {
			a.Steps[0].Status = StatusRunning
			o.Steps[0] = StepResult{Name: "prepare", Status: StatusSkipped}
		}},
		{"running-with-exit", func(_ *Attempt, o *Observation) { o.Status = StatusRunning; o.Artifacts = nil }},
		{"unfinished-terminal-step", func(_ *Attempt, o *Observation) { o.Steps[1] = StepResult{Name: "unit", Status: StatusRunning} }},
		{"log-offset", func(_ *Attempt, o *Observation) { o.LogOffset = 1 }},
		{"log-bound", func(_ *Attempt, o *Observation) { o.Logs = make([]byte, api.CILogPageMaxBytes+1) }},
		{"empty-page-with-more-logs", func(_ *Attempt, o *Observation) { o.MoreLogs = true }},
		{"artifact-traversal", func(_ *Attempt, o *Observation) { o.Artifacts[0].Path = "reports/../secret" }},
		{"artifact-undeclared", func(_ *Attempt, o *Observation) { o.Artifacts[0].Path = "other/unit.xml" }},
		{"artifact-absolute", func(_ *Attempt, o *Observation) { o.Artifacts[0].Path = "/reports/unit.xml" }},
		{"artifact-digest", func(_ *Attempt, o *Observation) { o.Artifacts[0].Digest = "mutable" }},
		{"artifact-negative-bytes", func(_ *Attempt, o *Observation) { o.Artifacts[0].Bytes = -1 }},
		{"artifact-size", func(_ *Attempt, o *Observation) { o.Artifacts[0].Bytes = api.CIArtifactMaxBytes + 1 }},
		{"artifact-count", func(_ *Attempt, o *Observation) {
			for range api.CIMaxArtifactsPerJob {
				o.Artifacts = append(o.Artifacts, o.Artifacts[0])
			}
		}},
		{"artifact-duplicate", func(_ *Attempt, o *Observation) { o.Artifacts = append(o.Artifacts, o.Artifacts[0]) }},
		{"artifact-byte-total", func(_ *Attempt, o *Observation) {
			o.Artifacts[0].Bytes = api.CIArtifactMaxBytes
			o.Artifacts = append(o.Artifacts, ArtifactRef{Path: "reports/two", Digest: o.Artifacts[0].Digest, Bytes: api.CIArtifactMaxBytes}, ArtifactRef{Path: "reports/three", Digest: o.Artifacts[0].Digest, Bytes: 1})
		}},
		{"artifact-before-stop", func(_ *Attempt, o *Observation) { o.Status = StatusRunning; o.ExitCode = nil }},
		{"terminal-backend-rewritten", func(a *Attempt, _ *Observation) { a.BackendStatus, a.ExitCode = StatusFailed, intPtr(1) }},
		{"terminal-manifest-rewritten", func(a *Attempt, _ *Observation) { a.BackendStatus, a.ExitCode = StatusSucceeded, intPtr(0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, o := clone(previous), clone(valid)
			tc.change(&a, &o)
			if err := validateObservation(plan.Jobs[0], a, o); !errors.Is(err, ErrBackendProtocol) {
				t.Fatalf("invalid backend report accepted: %v", err)
			}
		})
	}
}

func TestArtifactSelectorMatching(t *testing.T) {
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{
		{"reports/**", "reports/a/b.xml", true},
		{"reports/**/unit-*.xml", "reports/unit-a.xml", true},
		{"reports/**/unit-*.xml", "reports/a/b/unit-a.xml", true},
		{"reports/*.xml", "reports/a/b.xml", false},
		{"reports/**", "other/a.xml", false},
		{"**/out/**/a.txt", "out/a.txt", true},
		{"[", "a", false},
	} {
		if got := artifactMatches(tc.pattern, tc.file); got != tc.want {
			t.Errorf("%q matches %q = %v", tc.pattern, tc.file, got)
		}
	}
}
