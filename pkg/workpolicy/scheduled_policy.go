// Package workpolicy defines versioned, deterministic scheduling and failure
// decisions. It contains no database or VM operations; owners persist the
// decision with their fenced execution transition.
package workpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const Version = 1

type SchedulePolicy struct {
	Version              int    `json:"version" yaml:"version"`
	Overlap              string `json:"overlap" yaml:"overlap"`
	StartDeadlineSeconds int    `json:"start_deadline_seconds,omitempty" yaml:"start_deadline_seconds,omitempty"`
	MissedRuns           string `json:"missed_runs" yaml:"missed_runs"`
}

func (p SchedulePolicy) Validate() error {
	if p.Version != Version {
		return errors.New("schedule_policy.version must be 1")
	}
	if p.Overlap != "allow" && p.Overlap != "skip" && p.Overlap != "replace" {
		return errors.New("schedule_policy.overlap must be allow, skip, or replace")
	}
	if p.StartDeadlineSeconds < 0 {
		return errors.New("schedule_policy.start_deadline_seconds must be nonnegative")
	}
	if p.MissedRuns != "coalesce_latest" && p.MissedRuns != "skip" {
		return errors.New("schedule_policy.missed_runs must be coalesce_latest or skip")
	}
	return nil
}

func (p SchedulePolicy) Deadline(scheduledFor time.Time) *time.Time {
	if p.StartDeadlineSeconds == 0 {
		return nil
	}
	d := scheduledFor.UTC().Add(time.Duration(p.StartDeadlineSeconds) * time.Second)
	return &d
}

// A deadline is inclusive: starting exactly at the deadline is permitted.
func DeadlineMissed(deadline *time.Time, now time.Time) bool {
	return deadline != nil && now.After(*deadline)
}

type FailureRule struct {
	ExitCodes    []int    `json:"exit_codes,omitempty" yaml:"exit_codes,omitempty"`
	OutcomeCodes []string `json:"outcome_codes,omitempty" yaml:"outcome_codes,omitempty"`
	HTTPStatuses []int    `json:"http_statuses,omitempty" yaml:"http_statuses,omitempty"`
	Action       string   `json:"action" yaml:"action"`
}

type FailureRules struct {
	Version          int           `json:"version" yaml:"version"`
	Rules            []FailureRule `json:"rules" yaml:"rules"`
	UnmatchedFailure string        `json:"unmatched_failure" yaml:"unmatched_failure"`
	UncertainOutcome string        `json:"uncertain_outcome" yaml:"uncertain_outcome"`
}

func (p FailureRules) Validate() error {
	if p.Version != Version {
		return errors.New("failure_rules.version must be 1")
	}
	if !failureAction(p.UnmatchedFailure) {
		return errors.New("failure_rules.unmatched_failure must be retry or fail_partition")
	}
	if p.UncertainOutcome != "hold" && p.UncertainOutcome != "retry" {
		return errors.New("failure_rules.uncertain_outcome must be hold or retry")
	}
	exits, codes, statuses := map[int]bool{}, map[string]bool{}, map[int]bool{}
	for _, r := range p.Rules {
		if !failureAction(r.Action) || len(r.ExitCodes)+len(r.OutcomeCodes)+len(r.HTTPStatuses) == 0 {
			return errors.New("each failure rule needs a matcher and retry or fail_partition action")
		}
		if len(r.HTTPStatuses) > 0 {
			return errors.New("scheduled work currently supports exit_codes and outcome_codes matchers only")
		}
		for _, code := range r.ExitCodes {
			if code < 1 || code > 255 || exits[code] {
				return fmt.Errorf("exit code %d must be unique and between 1 and 255", code)
			}
			exits[code] = true
		}
		for _, code := range r.OutcomeCodes {
			if code == "" || len(code) > 64 || strings.TrimSpace(code) != code || codes[code] {
				return errors.New("outcome codes must be unique nonempty tokens of at most 64 bytes")
			}
			for _, c := range code {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
					return errors.New("outcome codes must use lowercase letters, digits, underscore, dash, or dot")
				}
			}
			codes[code] = true
		}
		for _, status := range r.HTTPStatuses {
			if status < 400 || status > 599 || statuses[status] {
				return errors.New("HTTP failure statuses must be unique and between 400 and 599")
			}
			statuses[status] = true
		}
	}
	return nil
}

func failureAction(action string) bool { return action == "retry" || action == "fail_partition" }

// Evidence distinguishes confirmed pre-execution infrastructure failures from
// missing completion receipts. Neither category can be asserted by a guest.
type Evidence struct {
	Succeeded   bool
	Cancelled   bool
	Infra       bool
	Uncertain   bool
	ExitCode    *int
	OutcomeCode string
	HTTPStatus  int
}

type Decision struct {
	Classification string `json:"classification"`
	Action         string `json:"action"`
	Reason         string `json:"reason"`
	PolicyVersion  int    `json:"policy_version"`
}

// Evaluate preserves the legacy generic retry policy for nil rules. Explicit
// rules never infer business retry safety from an unmapped exit failure.
func Evaluate(p *FailureRules, e Evidence) Decision {
	d := Decision{PolicyVersion: Version}
	switch {
	case e.Uncertain:
		d.Classification, d.Action, d.Reason = "uncertain", "hold", "completion_unknown"
		if p == nil || p.UncertainOutcome == "retry" {
			d.Action = "retry"
		}
	case e.Cancelled:
		d.Classification, d.Action, d.Reason = "cancelled", "complete", "cancellation_confirmed"
	case e.Infra:
		d.Classification, d.Action, d.Reason = "infrastructure", "retry", "pre_execution_failure"
	default:
		if p != nil {
			// A structured application result is authoritative even when the
			// process exits zero. It lets commands report a permanent record-level
			// rejection without encoding business semantics in a process exit code.
			if e.OutcomeCode != "" {
				for _, r := range p.Rules {
					for _, code := range r.OutcomeCodes {
						if code == e.OutcomeCode {
							d.Action, d.Reason = r.Action, "outcome_code_matched"
							goto classify
						}
					}
				}
			}
		}
		if e.Succeeded {
			d.Classification, d.Action, d.Reason = "success", "complete", "execution_succeeded"
			break
		}
		d.Action, d.Reason = "retry", "legacy_failure"
		if p != nil {
			d.Action, d.Reason = p.UnmatchedFailure, "unmatched_failure"
			if e.ExitCode != nil {
				for _, r := range p.Rules {
					for _, code := range r.ExitCodes {
						if code == *e.ExitCode {
							d.Action, d.Reason = r.Action, "exit_code_matched"
							goto classify
						}
					}
				}
			}
			for _, r := range p.Rules {
				for _, status := range r.HTTPStatuses {
					if status == e.HTTPStatus {
						d.Action, d.Reason = r.Action, "http_status_matched"
						goto classify
					}
				}
			}
		}
	classify:
		d.Classification = "permanent"
		if d.Action == "retry" {
			d.Classification = "retryable"
		}
	}
	if p == nil {
		d.PolicyVersion = 0
	}
	return d
}

// Clone prevents caller-owned slices from mutating an admitted policy.
func Clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	var result T
	_ = json.Unmarshal(b, &result)
	return &result
}
