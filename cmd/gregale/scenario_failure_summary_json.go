package main

import "encoding/json"

// MarshalJSON derives summaries from raw receipts, including nested runs.
// Decoding deliberately ignores saved summaries, so stale or edited derived
// fields cannot override the assertions used by HTML, replay or comparison.
func (r testRunReceipt) MarshalJSON() ([]byte, error) {
	type rawReceipt testRunReceipt
	return json.Marshal(struct {
		rawReceipt
		Summary *testFailureSummary `json:"failure_summary,omitempty"`
	}{rawReceipt: rawReceipt(r), Summary: r.FailureSummary()})
}

type testFailureAssertionSummary struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Request  string   `json:"request,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Expected *float64 `json:"expected,omitempty"`
	Observed *float64 `json:"observed,omitempty"`
	Evidence any      `json:"evidence,omitempty"`
}

type testFailureFaultRecord struct {
	Kind    string `json:"kind"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
	Port    int    `json:"port,omitempty"`
	Matches int64  `json:"matches"`
	Minimum int64  `json:"minimum"`
}

type testFailureRecoveryRecord struct {
	Phase    string                    `json:"phase"`
	Request  string                    `json:"request,omitempty"`
	Evidence *testLoadRecoveryEvidence `json:"evidence"`
}

func testFailureFaultRecords(e *testChaosEvidence) []testFailureFaultRecord {
	if e == nil {
		return nil
	}
	var records []testFailureFaultRecord
	for _, m := range e.Matches {
		records = append(records, testFailureFaultRecord{
			Kind: m.Rule.Kind, From: m.Rule.From, To: m.Rule.To, Port: m.Rule.Port, Matches: m.Matches, Minimum: m.MinMatches,
		})
	}
	return records
}

func testFailureRecoveryRecords(p testLoadChaosPhaseEvidence) []testFailureRecoveryRecord {
	var records []testFailureRecoveryRecord
	if p.Recovery != nil {
		records = append(records, testFailureRecoveryRecord{Phase: p.Name, Evidence: p.Recovery})
	}
	for _, name := range testEndpointRecoveryNames(p.StepRecovery) {
		records = append(records, testFailureRecoveryRecord{Phase: p.Name, Request: name, Evidence: p.StepRecovery[name]})
	}
	return records
}

// Preserve the same priority as testPhaseComparisonAssertions; never parse the
// human-readable finding to recover an assertion name or numerical measurement.
func firstTestFailureAssertion(p testLoadChaosPhaseEvidence) *testFailureAssertionSummary {
	if a := testFailureThresholdAssertion("", p.Thresholds); a != nil {
		return a
	}
	for _, step := range p.Steps {
		if a := testFailureThresholdAssertion(step.Name, step.Thresholds); a != nil {
			return a
		}
	}
	if a := testFailureAttemptAssertion("http_attempts", p.RetryAmplification); a != nil {
		return a
	}
	if a := testFailureAttemptAssertion("tcp_attempts", p.ConnectionAttempts); a != nil {
		return a
	}
	if e := p.Recovery; e != nil && !e.Passed {
		return &testFailureAssertionSummary{Kind: "recovery", Name: "recovery", Evidence: e}
	}
	for _, name := range testEndpointRecoveryNames(p.StepRecovery) {
		if e := p.StepRecovery[name]; e == nil || !e.Passed {
			return &testFailureAssertionSummary{Kind: "recovery", Name: "step_recovery", Request: name, Evidence: e}
		}
	}
	return testFailureResourceAssertion(p)
}

func testFailureThresholdAssertion(request string, checks []testLoadThresholdEvidence) *testFailureAssertionSummary {
	for _, e := range checks {
		if !e.Passed {
			return &testFailureAssertionSummary{
				Kind: "threshold", Name: e.Metric, Request: request, Operator: e.Operator,
				Expected: testPhaseComparisonNumber(e.Limit), Observed: testPhaseComparisonNumber(e.Actual), Evidence: e,
			}
		}
	}
	return nil
}

func testFailureAttemptAssertion(kind string, e *testRetryAmplificationEvidence) *testFailureAssertionSummary {
	if e == nil {
		return nil
	}
	if !e.Passed {
		a := &testFailureAssertionSummary{Kind: kind, Name: "attempts_per_request", Request: e.Request, Operator: "<=", Expected: testPhaseComparisonNumber(e.MaxAttemptsPerRequest), Evidence: e}
		if e.Requests > 0 {
			a.Observed = testPhaseComparisonNumber(e.AttemptsPerRequest)
		}
		return a
	}
	if e.PerRequest != nil && !e.PerRequest.Passed {
		return &testFailureAssertionSummary{
			Kind: "per_request_attempts", Name: "max_attempts", Request: e.Request, Operator: "<=",
			Expected: testPhaseComparisonNumber(float64(e.PerRequest.Limit)), Observed: testPhaseComparisonNumber(float64(e.PerRequest.MaxAttempts)), Evidence: e.PerRequest,
		}
	}
	return nil
}

func testFailureResourceAssertion(p testLoadChaosPhaseEvidence) *testFailureAssertionSummary {
	for _, name := range testEndpointRecoveryNames(p.RequestDeadlines) {
		if e := p.RequestDeadlines[name]; e == nil || !e.Passed {
			return &testFailureAssertionSummary{Kind: "request_deadline", Name: "request_deadlines", Request: name, Evidence: e}
		}
	}
	if e := p.CircuitBreaker; e != nil && !e.Passed {
		return &testFailureAssertionSummary{Kind: "circuit_breaker", Name: "circuit_breaker", Evidence: e}
	}
	if e := p.ConnectionLeaks; e != nil && !e.Passed {
		return &testFailureAssertionSummary{Kind: "connection_leaks", Name: "connection_leaks", Evidence: e}
	}
	if e := p.PoolExhaustion; e != nil && !e.Passed {
		return &testFailureAssertionSummary{Kind: "pool_exhaustion", Name: "pool_exhaustion", Evidence: e}
	}
	return nil
}
