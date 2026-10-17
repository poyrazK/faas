// adr: 1299
package automationgate_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

func gateFixture() (automationgate.Selection, api.AutomationLiveSuiteComparison) {
	s := automationgate.Selection{Name: "report", BaselineID: "00000000-0000-4000-8000-000000000001", CandidateID: "00000000-0000-4000-8000-000000000002", Expected: automationgate.Target{Version: 7, PublishedVersion: 4, DeploymentID: "00000000-0000-4000-8000-000000000003"}}
	now := time.Now().UTC()
	summary := api.AutomationLiveSuiteSummary{Name: s.Name, Version: 7, PublishedVersion: 4, DeploymentID: s.Expected.DeploymentID, CreatedAt: now, UpdatedAt: now}
	view := api.AutomationLiveSuiteComparison{Baseline: summary, Candidate: summary, BaselineOutdated: true, ObservedAt: now, Cases: []api.AutomationLiveSuiteCaseComparison{
		{Name: "steady", Change: "unchanged", Baseline: observation("passed"), Candidate: observation("passed")},
		{Name: "recovered", Change: "recovery", Baseline: observation("failed"), Candidate: observation("passed")},
	}}
	view.Baseline.ID, view.Candidate.ID = s.BaselineID, s.CandidateID
	recount(&view)
	return s, view
}

func observation(result string) *api.AutomationLiveSuiteObservation {
	run := uuid.NewString()
	if result == "not_started" {
		run = ""
	}
	return &api.AutomationLiveSuiteObservation{RunID: run, Result: result}
}

func recount(view *api.AutomationLiveSuiteComparison) {
	view.Summary = api.AutomationLiveSuiteComparisonCounts{Total: len(view.Cases)}
	view.Baseline.CaseCount, view.Baseline.LinkedRuns = 0, 0
	view.Candidate.CaseCount, view.Candidate.LinkedRuns = 0, 0
	for _, entry := range view.Cases {
		for i, side := range []*api.AutomationLiveSuiteObservation{entry.Baseline, entry.Candidate} {
			if side == nil {
				continue
			}
			summary := &view.Baseline
			if i == 1 {
				summary = &view.Candidate
			}
			summary.CaseCount++
			if side.RunID != "" {
				summary.LinkedRuns++
			}
		}
		switch entry.Change {
		case "regression":
			view.Summary.Regressions++
		case "recovery":
			view.Summary.Recoveries++
		case "unchanged":
			view.Summary.Unchanged++
		case "added":
			view.Summary.Added++
		case "removed":
			view.Summary.Removed++
		case "unknown":
			view.Summary.Unknown++
		case "incompatible":
			view.Summary.Incompatible++
		}
	}
}

func TestStrictGatePolicy(t *testing.T) {
	for _, test := range []struct {
		name, change, before, after string
		code                        int
		reasons                     []string
	}{
		{"unchanged-pass", "unchanged", "passed", "passed", 0, []string{}},
		{"recovery", "recovery", "failed", "passed", 0, []string{}},
		{"regression", "regression", "passed", "failed", 1, []string{"candidate_failed", "regressions"}},
		{"unchanged-failure", "unchanged", "failed", "failed", 1, []string{"candidate_failed"}},
		{"added-pass", "added", "", "passed", 0, []string{}},
		{"added-failure", "added", "", "failed", 1, []string{"candidate_failed"}},
		{"added-pending", "added", "", "pending", 7, []string{"candidate_incomplete"}},
		{"added-expired", "added", "", "unavailable", 7, []string{"candidate_incomplete"}},
		{"added-unstarted", "added", "", "not_started", 7, []string{"candidate_incomplete"}},
		{"removed", "removed", "passed", "", 7, []string{"removed_cases"}},
		{"baseline-pending", "unknown", "pending", "passed", 7, []string{"unknown_results"}},
		{"baseline-expired", "unknown", "unavailable", "passed", 7, []string{"unknown_results"}},
		{"candidate-pending", "unknown", "passed", "pending", 7, []string{"candidate_incomplete", "unknown_results"}},
		{"candidate-unchecked", "unknown", "passed", "unavailable", 7, []string{"candidate_incomplete", "unknown_results"}},
		{"candidate-unstarted", "unknown", "passed", "not_started", 7, []string{"candidate_incomplete", "unknown_results"}},
		{"missing-hash", "unknown", "passed", "passed", 7, []string{"unknown_results"}},
		{"changed-checks", "incompatible", "passed", "passed", 7, []string{"incompatible_cases"}},
		{"changed-checks-failed", "incompatible", "passed", "failed", 1, []string{"candidate_failed", "incompatible_cases"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, view := gateFixture()
			entry := api.AutomationLiveSuiteCaseComparison{Name: "selected", Change: test.change}
			if test.before != "" {
				entry.Baseline = observation(test.before)
			}
			if test.after != "" {
				entry.Candidate = observation(test.after)
			}
			view.Cases[0] = entry
			recount(&view)
			got, err := automationgate.Evaluate(s, view)
			if err != nil || got.ExitCode != test.code || !reflect.DeepEqual(got.Reasons, test.reasons) {
				t.Fatalf("gate=%+v err=%v", got, err)
			}
			if got.ExitCode == 0 && (got.Outcome != "passed" || got.CandidatePassed != view.Candidate.CaseCount || got.CandidateFailed != 0) {
				t.Fatal("gate passed without complete passing candidate evidence", got)
			}
		})
	}
}

func TestStrictGateTargetAndFailurePrecedence(t *testing.T) {
	for _, mode := range []string{"version", "published", "deployment", "outdated", "wrong-target-failed", "current-failed-and-unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, view := gateFixture()
			want := 7
			switch mode {
			case "version", "wrong-target-failed":
				view.Candidate.Version++
			case "published":
				view.Candidate.PublishedVersion--
			case "deployment":
				view.Candidate.DeploymentID = uuid.NewString()
			case "outdated":
				view.CandidateOutdated = true
			case "current-failed-and-unknown":
				want = 1
				view.Cases[1].Change = "unknown"
				view.Cases[1].Candidate = observation("unavailable")
			}
			if mode == "wrong-target-failed" || mode == "current-failed-and-unknown" {
				view.Cases[0].Change = "regression"
				view.Cases[0].Candidate.Result = "failed"
			}
			recount(&view)
			got, err := automationgate.Evaluate(s, view)
			if err != nil || got.ExitCode != want {
				t.Fatal(mode, got, err)
			}
		})
	}
}

func TestStrictGateRejectsMalformedComparisons(t *testing.T) {
	edits := map[string]func(*api.AutomationLiveSuiteComparison){
		"wrong-baseline":     func(v *api.AutomationLiveSuiteComparison) { v.Baseline.ID = uuid.NewString() },
		"wrong-candidate":    func(v *api.AutomationLiveSuiteComparison) { v.Candidate.ID = uuid.NewString() },
		"foreign-name":       func(v *api.AutomationLiveSuiteComparison) { v.Baseline.Name = "foreign" },
		"invalid-deployment": func(v *api.AutomationLiveSuiteComparison) { v.Candidate.DeploymentID = "invalid" },
		"invalid-version":    func(v *api.AutomationLiveSuiteComparison) { v.Baseline.PublishedVersion = 8 },
		"missing-time":       func(v *api.AutomationLiveSuiteComparison) { v.ObservedAt = time.Time{} },
		"missing-created":    func(v *api.AutomationLiveSuiteComparison) { v.Candidate.CreatedAt = time.Time{} },
		"old-updated":        func(v *api.AutomationLiveSuiteComparison) { v.Candidate.UpdatedAt = time.Time{} },
		"empty-cases":        func(v *api.AutomationLiveSuiteComparison) { v.Cases = nil },
		"false-counts":       func(v *api.AutomationLiveSuiteComparison) { v.Summary.Recoveries = 0 },
		"negative-count":     func(v *api.AutomationLiveSuiteComparison) { v.Summary.Unknown = -1 },
		"missing-case":       func(v *api.AutomationLiveSuiteComparison) { v.Cases = v.Cases[:1] },
		"over-limit": func(v *api.AutomationLiveSuiteComparison) {
			v.Candidate.CaseCount = api.AutomationLiveSuiteMaxCases + 1
		},
		"false-links":      func(v *api.AutomationLiveSuiteComparison) { v.Candidate.LinkedRuns-- },
		"duplicate":        func(v *api.AutomationLiveSuiteComparison) { v.Cases[1].Name = v.Cases[0].Name },
		"empty-name":       func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Name = "" },
		"control-name":     func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Name = "bad\nname" },
		"future-change":    func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Change = "future" },
		"future-result":    func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Candidate.Result = "future" },
		"invalid-run":      func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Candidate.RunID = "bad" },
		"missing-run":      func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Candidate.RunID = "" },
		"no-sides":         func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Baseline, v.Cases[0].Candidate = nil, nil },
		"false-add":        func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Change = "added" },
		"false-remove":     func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Change = "removed" },
		"false-regression": func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Change = "regression" },
		"false-recovery":   func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Change = "recovery" },
		"false-unchanged":  func(v *api.AutomationLiveSuiteComparison) { v.Cases[1].Change = "unchanged" },
		"unstarted-run":    func(v *api.AutomationLiveSuiteComparison) { v.Cases[0].Candidate.Result = "not_started" },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			s, view := gateFixture()
			edit(&view)
			switch name {
			case "future-change", "future-result", "no-sides", "false-add", "false-remove", "false-regression", "false-recovery", "false-unchanged", "unstarted-run":
				recount(&view)
			}
			if got, err := automationgate.Evaluate(s, view); err == nil {
				t.Fatal("invalid evidence accepted", got)
			}
		})
	}
}

func TestStrictGateRequiresExplicitDistinctSelection(t *testing.T) {
	for _, mode := range []string{"same-suite", "empty-name", "bad-baseline", "bad-candidate", "zero-version", "zero-published", "published-after-version", "nil-deployment"} {
		t.Run(mode, func(t *testing.T) {
			s, view := gateFixture()
			switch mode {
			case "same-suite":
				s.CandidateID = s.BaselineID
			case "empty-name":
				s.Name = ""
			case "bad-baseline":
				s.BaselineID = "invalid"
			case "bad-candidate":
				s.CandidateID = uuid.Nil.String()
			case "zero-version":
				s.Expected.Version = 0
			case "zero-published":
				s.Expected.PublishedVersion = 0
			case "published-after-version":
				s.Expected.PublishedVersion = 8
			case "nil-deployment":
				s.Expected.DeploymentID = uuid.Nil.String()
			}
			if _, err := automationgate.Evaluate(s, view); err == nil {
				t.Fatal("ambiguous selection accepted")
			}
		})
	}
}

func TestStrictGateBoundedUnion(t *testing.T) {
	s, view := gateFixture()
	view.Cases = nil
	for i := range api.AutomationLiveSuiteMaxCases {
		view.Cases = append(view.Cases,
			api.AutomationLiveSuiteCaseComparison{Name: fmt.Sprintf("added %d", i), Change: "added", Candidate: observation("passed")},
			api.AutomationLiveSuiteCaseComparison{Name: fmt.Sprintf("removed %d", i), Change: "removed", Baseline: observation("passed")})
	}
	recount(&view)
	got, err := automationgate.Evaluate(s, view)
	if err != nil || got.ExitCode != 7 || got.CandidatePassed != api.AutomationLiveSuiteMaxCases {
		t.Fatal(got, err)
	}
	view.Cases = append(view.Cases, view.Cases[0])
	if _, err := automationgate.Evaluate(s, view); err == nil {
		t.Fatal("oversized union accepted")
	}
}
