package main

import (
	"fmt"
	"io"
	"math"
)

// testLoadPhaseComparison is a presentation of existing start-attributed phase
// metrics. It adds no assertion and never changes a scenario's verdict.
type testLoadPhaseComparison struct {
	Name                     string
	Role                     string
	Requests                 int
	P95MS                    *float64
	ErrorRatePercent         *float64
	RequestsPerSecond        *float64
	P95ChangePercent         *float64
	ErrorRateChangePoints    *float64
	RequestRateChangePercent *float64
	Attempts                 []string
	Recovery                 []string
	Findings                 []string
}

// PhaseComparisons supplies the shared derived view to the HTML template.
func (report *testLoadEvidence) PhaseComparisons() []testLoadPhaseComparison {
	return testLoadPhaseComparisons(report)
}

func testLoadPhaseRole(index int, phases []testLoadChaosPhase) string {
	if len(phases[index].Rules) > 0 {
		return "fault"
	}
	if index == 0 {
		return "baseline"
	}
	if len(phases[index-1].Rules) > 0 {
		return "recovery"
	}
	return "control"
}

func testPhaseComparisonNumber(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func testPhaseComparisonPercent(baseline, current *float64) *float64 {
	if baseline == nil || current == nil || *baseline <= 0 {
		return nil
	}
	return testPhaseComparisonNumber((*current / *baseline - 1) * 100)
}

// Every row uses the original baseline. A later recovery must not silently
// replace it and hide cumulative degradation across an outage campaign.
func testLoadPhaseComparisons(report *testLoadEvidence) []testLoadPhaseComparison {
	if report == nil || len(report.ChaosPhases) == 0 {
		return nil
	}
	rows := make([]testLoadPhaseComparison, 0, len(report.ChaosPhases))
	for i := range report.ChaosPhases {
		phase := &report.ChaosPhases[i]
		row := testLoadPhaseComparison{Name: phase.Name, Role: phase.Role, Requests: phase.Requests}
		if row.Role == "" {
			row.Role = testRecordedPhaseRole(i, report.ChaosPhases)
		}
		if phase.Requests > 0 {
			if phase.Latency.Samples == phase.Requests && phase.Latency.P95MS >= 0 {
				row.P95MS = testPhaseComparisonNumber(phase.Latency.P95MS)
			}
			if phase.Failures >= 0 && phase.Failures <= phase.Requests {
				row.ErrorRatePercent = testPhaseComparisonNumber(100 * float64(phase.Failures) / float64(phase.Requests))
			}
		}
		if phase.ActualDurationMS > 0 && phase.Requests >= 0 {
			row.RequestsPerSecond = testPhaseComparisonNumber(1000 * float64(phase.Requests) / float64(phase.ActualDurationMS))
		}
		if i > 0 && rows[0].Role == "baseline" {
			row.P95ChangePercent = testPhaseComparisonPercent(rows[0].P95MS, row.P95MS)
			row.RequestRateChangePercent = testPhaseComparisonPercent(rows[0].RequestsPerSecond, row.RequestsPerSecond)
			if rows[0].ErrorRatePercent != nil && row.ErrorRatePercent != nil {
				row.ErrorRateChangePoints = testPhaseComparisonNumber(*row.ErrorRatePercent - *rows[0].ErrorRatePercent)
			}
		}
		testPhaseComparisonAssertions(&row, phase)
		rows = append(rows, row)
	}
	return rows
}

// Older receipts have no explicit role. Infer it only from recorded rule or
// recovery evidence, never from user-chosen phase names.
func testRecordedPhaseRole(index int, phases []testLoadChaosPhaseEvidence) string {
	fault := func(phase testLoadChaosPhaseEvidence) bool {
		return phase.Chaos != nil && (len(phase.Chaos.Rules) > 0 || len(phase.Chaos.Matches) > 0)
	}
	if fault(phases[index]) {
		return "fault"
	}
	if index == 0 {
		return "baseline"
	}
	if fault(phases[index-1]) || phases[index].Recovery != nil || len(phases[index].StepRecovery) > 0 {
		return "recovery"
	}
	return "control"
}

func testPhaseComparisonAssertions(row *testLoadPhaseComparison, phase *testLoadChaosPhaseEvidence) {
	for _, threshold := range phase.Thresholds {
		testPhaseComparisonThreshold(row, "", threshold)
	}
	for _, step := range phase.Steps {
		for _, threshold := range step.Thresholds {
			testPhaseComparisonThreshold(row, step.Name+".", threshold)
		}
	}
	testPhaseComparisonAttempts(row, "HTTP", phase.RetryAmplification)
	testPhaseComparisonAttempts(row, "TCP", phase.ConnectionAttempts)
	testPhaseComparisonRecovery(row, "aggregate", phase.Recovery)
	for _, name := range testEndpointRecoveryNames(phase.StepRecovery) {
		testPhaseComparisonRecovery(row, name, phase.StepRecovery[name])
	}
	for _, name := range testEndpointRecoveryNames(phase.RequestDeadlines) {
		e := phase.RequestDeadlines[name]
		if e == nil {
			row.Findings = append(row.Findings, name+" response deadline: evidence unavailable")
		} else if !e.Passed {
			row.Findings = append(row.Findings, fmt.Sprintf("%s response deadline: %s", name, e.Error))
		}
	}
	if e := phase.CircuitBreaker; e != nil && !e.Passed {
		row.Findings = append(row.Findings, "circuit breaker: "+e.Error)
	}
	if e := phase.ConnectionLeaks; e != nil && !e.Passed {
		row.Findings = append(row.Findings, "connection recovery: "+e.Error)
	}
	if e := phase.PoolExhaustion; e != nil && !e.Passed {
		row.Findings = append(row.Findings, "pool recovery: "+e.Error)
	}
}

func testPhaseComparisonThreshold(row *testLoadPhaseComparison, prefix string, e testLoadThresholdEvidence) {
	if !e.Passed {
		row.Findings = append(row.Findings, fmt.Sprintf("%s%s %s %.4g, observed %.4g", prefix, e.Metric, e.Operator, e.Limit, e.Actual))
	}
}

func testPhaseComparisonAttempts(row *testLoadPhaseComparison, kind string, e *testRetryAmplificationEvidence) {
	if e == nil {
		return
	}
	if e.Requests > 0 {
		row.Attempts = append(row.Attempts, fmt.Sprintf("%s %s -> %s (%s): %d attempts / %d requests = %.3f; limit %.3f", kind, e.From, e.To, e.Request, e.Attempts, e.Requests, e.AttemptsPerRequest, e.MaxAttemptsPerRequest))
	} else {
		row.Attempts = append(row.Attempts, kind+" attempts: unavailable (no incoming samples)")
	}
	if !e.Passed {
		row.Findings = append(row.Findings, kind+" attempt budget: "+e.Error)
	}
	if strict := e.PerRequest; strict != nil {
		row.Attempts = append(row.Attempts, fmt.Sprintf("per-request maximum %d; limit %d; %d/%d correlated; %d violations", strict.MaxAttempts, strict.Limit, strict.Correlated, strict.Requests, strict.ViolationCount))
		if !strict.Passed {
			row.Findings = append(row.Findings, "per-request attempt budget: "+strict.Error)
		}
	}
}

func testPhaseComparisonRecovery(row *testLoadPhaseComparison, name string, e *testLoadRecoveryEvidence) {
	if e == nil {
		if name != "aggregate" {
			row.Findings = append(row.Findings, name+" recovery: evidence unavailable")
		}
		return
	}
	observed := "unconfirmed"
	if e.RecoveryMS != nil {
		observed = fmt.Sprintf("%.3fms", *e.RecoveryMS)
	}
	row.Recovery = append(row.Recovery, fmt.Sprintf("%s: %s; within %dms; passed %t", name, observed, e.WithinMS, e.Passed))
	if !e.Passed {
		row.Findings = append(row.Findings, name+" recovery: "+e.Error)
	}
}

func printTestLoadPhaseComparisons(out io.Writer, report *testLoadEvidence) {
	rows := testLoadPhaseComparisons(report)
	if len(rows) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "  Baseline / fault / recovery comparison (informational; requests attributed by start time)")
	for _, row := range rows {
		_, _ = fmt.Fprintf(out, "    %s [%s]: %d samples; p95 %s ms (%s%%); errors %s%% (%s pp); request rate %s/s (%s%%)\n",
			row.Name, row.Role, row.Requests,
			testPhaseComparisonValue(row.P95MS), testPhaseComparisonValue(row.P95ChangePercent),
			testPhaseComparisonValue(row.ErrorRatePercent), testPhaseComparisonValue(row.ErrorRateChangePoints),
			testPhaseComparisonValue(row.RequestsPerSecond), testPhaseComparisonValue(row.RequestRateChangePercent))
		for _, text := range append(append(append([]string{}, row.Attempts...), row.Recovery...), row.Findings...) {
			_, _ = fmt.Fprintln(out, "      "+text)
		}
	}
	_, _ = fmt.Fprintln(out, "    Changes use the initial baseline; unavailable values are not zero or passing evidence.")
}

func testPhaseComparisonValue(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.3f", *value)
}
