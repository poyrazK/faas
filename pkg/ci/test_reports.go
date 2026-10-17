package ci

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ReportParsed      = "parsed"
	ReportMissing     = "missing"
	ReportInvalid     = "invalid"
	ReportLimit       = "limit_exceeded"
	ReportUncollected = "uncollected"
)

// Test reports are diagnostics. Native task receipts and exit codes retain
// execution authority; no report field can launch, retry or finish a command.
type TestCounts struct {
	Tests   int `json:"tests"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errors  int `json:"errors"`
	Skipped int `json:"skipped"`
}

type TestFailure struct {
	Index   int    `json:"index"`
	Suite   string `json:"suite,omitempty"`
	Class   string `json:"class,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type TestReport struct {
	ArtifactRef
	Status string `json:"status"`
	TestCounts
	Failures  []TestFailure `json:"failures,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
}

type TestReportTotals struct {
	Expected int `json:"expected_reports"`
	Parsed   int `json:"parsed_reports"`
	Problems int `json:"report_problems"`
	TestCounts
}

type JobTestReports struct {
	Job       string           `json:"job"`
	Status    string           `json:"status"`
	State     string           `json:"collection_state"`
	AttemptID string           `json:"attempt_id,omitempty"`
	Totals    TestReportTotals `json:"totals"`
	Reports   []TestReport     `json:"reports"`
}

type ExecutionTestReports struct {
	ExecutionID string           `json:"execution_id"`
	CommitSHA   string           `json:"commit_sha"`
	Status      string           `json:"status"`
	Totals      TestReportTotals `json:"totals"`
	Jobs        []JobTestReports `json:"jobs"`
}

func normalizeJUnit(job *Job) error {
	if len(job.JUnit) > api.CIJUnitMaxReportsPerJob {
		return fmt.Errorf("job has too many JUnit report paths")
	}
	slices.Sort(job.JUnit)
	for i, name := range job.JUnit {
		if !validArtifact(name) || strings.ContainsAny(name, "*?[") || !JobsArtifactSelected(job.Artifacts, name) || i > 0 && name == job.JUnit[i-1] {
			return fmt.Errorf("JUnit paths must be unique literal paths selected by the job's artifacts")
		}
	}
	return nil
}

func completeObservationReports(plan PlannedJob, out Observation) Observation {
	if !terminal(out.Status) || len(out.TestReports) != 0 {
		return out
	}
	for _, name := range plan.JUnit {
		report := TestReport{ArtifactRef: ArtifactRef{Path: name}, Status: ReportMissing}
		for _, artifact := range out.Artifacts {
			if artifact.Path == name {
				report.ArtifactRef, report.Status = artifact, ReportUncollected
			}
		}
		out.TestReports = append(out.TestReports, report)
	}
	return out
}

func validateTestReports(plan PlannedJob, out Observation) error {
	if len(plan.JUnit) > api.CIJUnitMaxReportsPerJob {
		return protocolError("too many test report paths")
	}
	expected := 0
	if terminal(out.Status) {
		expected = len(plan.JUnit)
	}
	if len(out.TestReports) != expected {
		return protocolError("test report count or phase changed")
	}
	for i, report := range out.TestReports {
		if report.Path != plan.JUnit[i] {
			return protocolError("test report path changed")
		}
		found := false
		for _, artifact := range out.Artifacts {
			if artifact == report.ArtifactRef {
				found = true
			}
		}
		if report.Status == ReportMissing {
			for _, artifact := range out.Artifacts {
				if artifact.Path == report.Path {
					return protocolError("existing artifact reported missing")
				}
			}
			if report.Digest != "" || report.Bytes != 0 {
				return protocolError("missing report contains artifact identity")
			}
		} else if !found {
			return protocolError("test report artifact identity changed")
		}
		if err := validateReportData(report); err != nil {
			return err
		}
	}
	return nil
}

func validateReportData(report TestReport) error {
	if report.Status != ReportParsed {
		if report.Status != ReportMissing && report.Status != ReportInvalid && report.Status != ReportLimit && report.Status != ReportUncollected {
			return protocolError("unknown test report status")
		}
		if report.TestCounts != (TestCounts{}) || len(report.Failures) != 0 || report.Truncated {
			return protocolError("unparsed report contains test results")
		}
	}
	for _, n := range []int{report.Tests, report.Passed, report.Failed, report.Errors, report.Skipped} {
		if n < 0 || n > api.CIJUnitMaxCases {
			return protocolError("test count exceeds bound")
		}
	}
	if report.Tests != report.Passed+report.Failed+report.Errors+report.Skipped || len(report.Failures) > api.CIJUnitMaxFailures {
		return protocolError("inconsistent test report counts")
	}
	failed, errored, seen := 0, 0, map[int]bool{}
	for _, failure := range report.Failures {
		if failure.Index < 1 || failure.Index > report.Tests || seen[failure.Index] {
			return protocolError("invalid failure index")
		}
		seen[failure.Index] = true
		switch failure.Kind {
		case "failure":
			failed++
		case "error":
			errored++
		default:
			return protocolError("unknown test failure kind")
		}
		for _, field := range []struct {
			text  string
			bound int
		}{
			{failure.Suite, api.CIJUnitNameMaxBytes}, {failure.Class, api.CIJUnitNameMaxBytes}, {failure.Name, api.CIJUnitNameMaxBytes},
			{failure.Type, api.CIJUnitNameMaxBytes}, {failure.Message, api.CIJUnitMessageMaxBytes}, {failure.Detail, api.CIJUnitDetailMaxBytes},
		} {
			if cleaned, _ := clipReportText(field.text, field.bound); cleaned != field.text {
				return protocolError("invalid test failure text")
			}
		}
	}
	if failed > report.Failed || errored > report.Errors || !report.Truncated && len(report.Failures) != report.Failed+report.Errors {
		return protocolError("inconsistent retained test failures")
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > api.CIJUnitSnapshotMaxBytes {
		return protocolError("test report snapshot exceeds bound")
	}
	return nil
}

func clipReportText(text string, bound int) (string, bool) {
	var out strings.Builder
	changed := false
	for _, r := range text {
		if !unicode.IsGraphic(r) && r != '\n' && r != '\t' {
			changed = true
			continue
		}
		if out.Len()+utf8.RuneLen(r) > bound {
			changed = true
			break
		}
		out.WriteRune(r)
	}
	return out.String(), changed
}

func reportTotals(expected int, reports []TestReport) TestReportTotals {
	out := TestReportTotals{Expected: expected}
	for _, r := range reports {
		if r.Status != ReportParsed {
			out.Problems++
			continue
		}
		out.Parsed++
		out.Tests += r.Tests
		out.Passed += r.Passed
		out.Failed += r.Failed
		out.Errors += r.Errors
		out.Skipped += r.Skipped
	}
	return out
}

// ExecutionReports projects only report diagnostics, never source authority,
// commands, object keys or credentials. It performs no provider I/O.
func ExecutionReports(e Execution) (ExecutionTestReports, error) {
	out := ExecutionTestReports{ExecutionID: e.ID, CommitSHA: e.CommitSHA, Status: e.Status, Jobs: make([]JobTestReports, 0)}
	configured := false
	for _, plan := range e.Plan.Jobs {
		configured = configured || len(plan.JUnit) > 0
	}
	if !configured {
		return out, nil
	}
	if len(e.Jobs) != len(e.Plan.Jobs) {
		return out, ErrConflict
	}
	for i, job := range e.Jobs {
		plan := e.Plan.Jobs[i]
		if job.Name != plan.Name {
			return out, ErrConflict
		}
		if len(plan.JUnit) == 0 {
			continue
		}
		item := JobTestReports{Job: job.Name, Status: job.Status, State: "pending", Reports: make([]TestReport, 0)}
		if job.Attempt != nil {
			a := job.Attempt
			observation := completeObservationReports(plan, Observation{Status: a.BackendStatus, Artifacts: a.Artifacts, TestReports: a.TestReports})
			if err := validateTestReports(plan, observation); err != nil {
				return out, err
			}
			item.AttemptID, item.Reports = a.ID, clone(observation.TestReports)
			if len(item.Reports) > 0 {
				item.State = "complete"
			}
		} else if terminal(job.Status) {
			item.State = "not_run"
		}
		item.Totals = reportTotals(len(plan.JUnit), item.Reports)
		if item.Totals.Problems > 0 {
			item.State = "incomplete"
		}
		out.Totals.Expected += item.Totals.Expected
		out.Totals.Parsed += item.Totals.Parsed
		out.Totals.Problems += item.Totals.Problems
		out.Totals.Tests += item.Totals.Tests
		out.Totals.Passed += item.Totals.Passed
		out.Totals.Failed += item.Totals.Failed
		out.Totals.Errors += item.Totals.Errors
		out.Totals.Skipped += item.Totals.Skipped
		out.Jobs = append(out.Jobs, item)
	}
	return out, nil
}
