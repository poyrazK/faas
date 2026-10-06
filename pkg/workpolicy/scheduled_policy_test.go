package workpolicy

import (
	"testing"
	"time"
)

func TestFailureDecisions(t *testing.T) {
	policy := &FailureRules{Version: 1, UnmatchedFailure: "fail_partition", UncertainOutcome: "hold", Rules: []FailureRule{
		{ExitCodes: []int{65}, Action: "fail_partition"},
		{ExitCodes: []int{75}, Action: "retry"},
	}}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	outcomePolicy := &FailureRules{Version: 1, UnmatchedFailure: "retry", UncertainOutcome: "hold", Rules: []FailureRule{
		{OutcomeCodes: []string{"invalid_record"}, Action: "fail_partition"},
	}}
	if err := outcomePolicy.Validate(); err != nil {
		t.Fatalf("outcome policy: %v", err)
	}
	httpPolicy := &FailureRules{Version: 1, UnmatchedFailure: "retry", UncertainOutcome: "hold", Rules: []FailureRule{
		{HTTPStatuses: []int{503}, Action: "retry"},
	}}
	if err := httpPolicy.Validate(); err == nil {
		t.Fatal("accepted unsupported HTTP status matcher")
	}
	code := func(n int) *int { return &n }
	for _, tc := range []struct {
		name                  string
		p                     *FailureRules
		e                     Evidence
		class, action, reason string
	}{
		{"permanent", policy, Evidence{ExitCode: code(65)}, "permanent", "fail_partition", "exit_code_matched"},
		{"transient", policy, Evidence{ExitCode: code(75)}, "retryable", "retry", "exit_code_matched"},
		{"unmapped HTTP", policy, Evidence{HTTPStatus: 500}, "permanent", "fail_partition", "unmatched_failure"},
		{"HTTP evaluator", httpPolicy, Evidence{HTTPStatus: 503}, "retryable", "retry", "http_status_matched"},
		{"specific outcome evaluator", outcomePolicy, Evidence{ExitCode: code(75), OutcomeCode: "invalid_record"}, "permanent", "fail_partition", "outcome_code_matched"},
		{"outcome overrides successful process exit", outcomePolicy, Evidence{Succeeded: true, ExitCode: code(0), OutcomeCode: "invalid_record"}, "permanent", "fail_partition", "outcome_code_matched"},
		{"unmapped outcome preserves success", outcomePolicy, Evidence{Succeeded: true, OutcomeCode: "accepted"}, "success", "complete", "execution_succeeded"},
		{"infrastructure", policy, Evidence{Infra: true}, "infrastructure", "retry", "pre_execution_failure"},
		{"lost receipt", policy, Evidence{Uncertain: true, Infra: true}, "uncertain", "hold", "completion_unknown"},
		{"legacy", nil, Evidence{ExitCode: code(65)}, "retryable", "retry", "legacy_failure"},
		{"success", policy, Evidence{Succeeded: true}, "success", "complete", "execution_succeeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.p, tc.e)
			if got.Classification != tc.class || got.Action != tc.action || got.Reason != tc.reason {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestRejectAmbiguousFailureRules(t *testing.T) {
	for _, rules := range [][]FailureRule{
		{{ExitCodes: []int{65}, Action: "retry"}, {ExitCodes: []int{65}, Action: "fail_partition"}},
		{{ExitCodes: []int{0}, Action: "retry"}},
		{{OutcomeCodes: []string{"infra error"}, Action: "retry"}},
		{{OutcomeCodes: []string{"invalid_record"}, Action: "retry"}, {OutcomeCodes: []string{"invalid_record"}, Action: "fail_partition"}},
		{{HTTPStatuses: []int{200}, Action: "retry"}},
		{{HTTPStatuses: []int{503}, Action: "retry"}},
		{{Action: "retry"}},
	} {
		p := FailureRules{Version: 1, Rules: rules, UnmatchedFailure: "retry", UncertainOutcome: "hold"}
		if p.Validate() == nil {
			t.Fatalf("accepted ambiguous/invalid rules: %+v", rules)
		}
	}
}

func TestScheduleDeadlineUsesOccurrenceAndAllowsBoundary(t *testing.T) {
	p := SchedulePolicy{Version: 1, Overlap: "skip", StartDeadlineSeconds: 120, MissedRuns: "coalesce_latest"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	scheduled := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	d := p.Deadline(scheduled)
	if !d.Equal(scheduled.Add(2*time.Minute)) || DeadlineMissed(d, *d) || !DeadlineMissed(d, d.Add(time.Nanosecond)) {
		t.Fatalf("deadline = %v", d)
	}
}

func TestAdmissionClonesFailureRules(t *testing.T) {
	p := &FailureRules{Rules: []FailureRule{{ExitCodes: []int{65}}}}
	c := Clone(p)
	p.Rules[0].ExitCodes[0] = 75
	if c.Rules[0].ExitCodes[0] != 65 {
		t.Fatal("admitted rules alias customer input")
	}
}
