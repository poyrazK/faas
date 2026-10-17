package ci

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var artifactDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// A cancellation tombstone may have no guest telemetry (including when Cancel
// wins before Start). Retain known terminal steps and close unfinished ones.
func cancelledSteps(previous Attempt, report Observation) Observation {
	if report.Status == StatusCancelled && len(report.Steps) == 0 {
		report.Steps = clone(previous.Steps)
		for i := range report.Steps {
			if !terminal(report.Steps[i].Status) {
				report.Steps[i].Status, report.Steps[i].ExitCode = StatusCancelled, nil
			}
		}
	}
	return report
}

func protocolError(detail string) error { return fmt.Errorf("%w: %s", ErrBackendProtocol, detail) }

func validateObservation(plan PlannedJob, previous Attempt, report Observation) error {
	if report.Status != StatusRunning && report.Status != StatusSucceeded && report.Status != StatusFailed && report.Status != StatusCancelled {
		return protocolError("unsupported attempt state")
	}
	if terminal(previous.BackendStatus) && (report.Status != previous.BackendStatus ||
		!sameExit(previous.ExitCode, report.ExitCode) || previous.OutputIncomplete != report.OutputIncomplete || !slices.Equal(previous.Artifacts, report.Artifacts) || !reflect.DeepEqual(previous.TestReports, report.TestReports)) {
		return protocolError("terminal backend result changed while draining output")
	}
	if len(report.Steps) != len(plan.Steps) || len(previous.Steps) != len(plan.Steps) {
		return protocolError("step count changed")
	}
	if report.LogOffset != len(previous.Logs) || len(report.Logs) > logLimit(&previous) || (report.MoreLogs && len(report.Logs) == 0 && logLimit(&previous) > 0) {
		return protocolError("invalid log offset or page")
	}
	if err := validateSteps(previous.Steps, report); err != nil {
		return err
	}
	if report.Status == StatusSucceeded && (report.ExitCode == nil || *report.ExitCode != 0) {
		return protocolError("success requires exit code zero")
	}
	if report.Status == StatusFailed && (report.ExitCode == nil || *report.ExitCode == 0) {
		return protocolError("failure requires a nonzero exit code")
	}
	if report.Status == StatusRunning && report.ExitCode != nil {
		return protocolError("running attempt has an exit code")
	}
	if err := validateArtifacts(plan.Artifacts, report); err != nil {
		return err
	}
	return validateTestReports(plan, report)
}

func validateSteps(previous []StepResult, report Observation) error {
	stopped := false
	for i, step := range report.Steps {
		before := previous[i]
		if step.Name != before.Name || !validStepState(step.Status) {
			return protocolError("unknown step or state")
		}
		if err := validateStepTiming(before, step); err != nil {
			return err
		}
		if terminal(before.Status) && (step.Status != before.Status || !sameExit(step.ExitCode, before.ExitCode)) {
			return protocolError("terminal step changed")
		}
		if before.Status == StatusRunning && (step.Status == StatusQueued || step.Status == StatusSkipped) {
			return protocolError("step progress regressed")
		}
		if stopped && (step.Status == StatusRunning || step.Status == StatusSucceeded || step.Status == StatusFailed) {
			return protocolError("steps ran out of order")
		}
		stopped = stopped || step.Status != StatusSucceeded
		if (step.Status == StatusSucceeded && (step.ExitCode == nil || *step.ExitCode != 0)) ||
			(step.Status == StatusFailed && (step.ExitCode == nil || *step.ExitCode == 0)) ||
			((step.Status == StatusQueued || step.Status == StatusRunning || step.Status == StatusSkipped) && step.ExitCode != nil) {
			return protocolError("step exit code disagrees with state")
		}
		if report.Status == StatusSucceeded && step.Status != StatusSucceeded {
			return protocolError("success contains unfinished or unsuccessful steps")
		}
		if terminal(report.Status) && !terminal(step.Status) {
			return protocolError("finished attempt contains unfinished steps")
		}
	}
	return nil
}

func sameStepTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

// Guest clocks are diagnostic only. Native deadlines, leases and stop receipts
// remain authoritative. Old runner results without timestamps remain valid.
func validateStepTiming(before, step StepResult) error {
	if before.StartedAt != nil && !sameStepTime(before.StartedAt, step.StartedAt) || terminal(before.Status) && (!sameStepTime(before.StartedAt, step.StartedAt) || !sameStepTime(before.FinishedAt, step.FinishedAt)) {
		return protocolError("step timing changed")
	}
	if step.StartedAt != nil && step.StartedAt.IsZero() || step.FinishedAt != nil && step.FinishedAt.IsZero() {
		return protocolError("invalid step time")
	}
	if (step.Status == StatusQueued || step.Status == StatusSkipped) && (step.StartedAt != nil || step.FinishedAt != nil) || step.Status == StatusRunning && step.FinishedAt != nil ||
		(step.Status == StatusSucceeded || step.Status == StatusFailed) && step.StartedAt != nil && step.FinishedAt == nil {
		return protocolError("step timing disagrees with state")
	}
	if step.FinishedAt != nil && (step.StartedAt == nil || step.FinishedAt.Before(*step.StartedAt) || step.FinishedAt.Sub(*step.StartedAt) > time.Duration(api.CIMaxTimeoutSeconds+api.CIJobsFinalizeSeconds)*time.Second) {
		return protocolError("invalid step duration")
	}
	return nil
}

func validStepState(status string) bool {
	return status == StatusQueued || status == StatusRunning || terminal(status)
}

func sameExit(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func validateArtifacts(selectors []string, report Observation) error {
	if len(report.Artifacts) > api.CIMaxArtifactsPerJob || (!terminal(report.Status) && len(report.Artifacts) != 0) {
		return protocolError("invalid artifact manifest size or phase")
	}
	seen, total := make(map[string]bool), int64(0)
	for _, artifact := range report.Artifacts {
		if !validArtifact(artifact.Path) || strings.ContainsAny(artifact.Path, "*?[") || seen[artifact.Path] ||
			!artifactDigestPattern.MatchString(artifact.Digest) || artifact.Bytes < 0 || artifact.Bytes > api.CIArtifactMaxBytes {
			return protocolError("invalid artifact reference")
		}
		matches := false
		for _, selector := range selectors {
			matches = matches || artifactMatches(selector, artifact.Path)
		}
		if !matches {
			return protocolError("artifact is outside declared selectors")
		}
		seen[artifact.Path], total = true, total+artifact.Bytes
		if total > api.CIArtifactsMaxBytesPerJob {
			return protocolError("artifact byte allowance exceeded")
		}
	}
	return nil
}

// Segment glob matching, with ** matching zero or more whole path segments.
// Dynamic programming bounds work even for repeated ** selectors.
func artifactMatches(pattern, name string) bool {
	p, n := strings.Split(pattern, "/"), strings.Split(name, "/")
	previous := make([]bool, len(n)+1)
	previous[0] = true
	for _, segment := range p {
		next := make([]bool, len(n)+1)
		if segment == "**" {
			next[0] = previous[0]
		}
		for j, part := range n {
			if segment == "**" {
				next[j+1] = previous[j+1] || next[j]
			} else {
				matched, err := path.Match(segment, part)
				next[j+1] = err == nil && matched && previous[j]
			}
		}
		previous = next
	}
	return previous[len(n)]
}
