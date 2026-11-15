package main

import "time"

type obsDeploymentEvidenceProgress struct {
	FirstObservedAt            *time.Time `json:"first_observed_at"`
	FirstBlockedAt             *time.Time `json:"first_blocked_at"`
	FirstPassedAt              *time.Time `json:"first_passed_at"`
	StateSince                 *time.Time `json:"state_since"`
	BlockersSince              *time.Time `json:"blockers_since"`
	ObservedBlockedSeconds     float64    `json:"observed_blocked_seconds"`
	ObservedUnavailableSeconds float64    `json:"observed_unavailable_seconds"`
}

type obsDeploymentEvidenceProgressObservation struct {
	summary              obsDeploymentEvidenceProgress
	state, reason, cause string
	status               *int
	at                   time.Time
	seen                 bool
}

func (r *obsDeploymentEvidenceRecorder) observeProgress(index int, check *obsDeploymentEvidenceCheck, cause string, status *int, at time.Time) {
	previous := &r.progress[index]
	progress := &previous.summary
	observed := at.UTC()
	if previous.seen && !at.Before(previous.at) {
		// Like the health timeline, attribute each sampled interval to the
		// preceding observation. Keep raw monotonic clocks for elapsed time.
		elapsed := max(0, at.Sub(previous.at).Seconds())
		switch previous.state {
		case "blocked":
			progress.ObservedBlockedSeconds += elapsed
		case "unavailable":
			progress.ObservedUnavailableSeconds += elapsed
		}
	}
	if !previous.seen {
		progress.FirstObservedAt = &observed
	}
	if !previous.seen || previous.state != check.State {
		progress.StateSince = &observed
	}
	if check.State == "blocked" {
		if progress.FirstBlockedAt == nil {
			progress.FirstBlockedAt = &observed
		}
		if previous.state != "blocked" || previous.cause != cause {
			progress.BlockersSince = &observed
		}
	} else {
		progress.BlockersSince = nil
	}
	if check.State == "passed" && progress.FirstPassedAt == nil {
		progress.FirstPassedAt = &observed
	}
	if !previous.seen || previous.state != check.State || previous.reason != check.Reason || previous.cause != cause || !sameObsDeploymentSourceStatus(previous.status, status) {
		r.history.append(obsDeploymentEvidenceEvent{At: observed, Check: check.Name, State: check.State, Reason: check.Reason, SourceHTTPStatus: obsDeploymentEvidencePtr(status), CauseSHA256: cause, Blockers: cloneObsDeploymentEvidencePreview(check.Blockers)})
	}
	// A regressing coordinator clock cannot make the next interval count twice.
	if !previous.seen || !at.Before(previous.at) {
		previous.at = at
	}
	previous.state, previous.reason, previous.cause, previous.status, previous.seen = check.State, check.Reason, cause, obsDeploymentEvidencePtr(status), true
	check.Progress = cloneObsDeploymentEvidenceProgress(progress)
}

func cloneObsDeploymentEvidenceProgress(in *obsDeploymentEvidenceProgress) *obsDeploymentEvidenceProgress {
	if in == nil {
		return nil
	}
	out := *in
	out.FirstObservedAt, out.FirstBlockedAt, out.FirstPassedAt = obsDeploymentEvidencePtr(in.FirstObservedAt), obsDeploymentEvidencePtr(in.FirstBlockedAt), obsDeploymentEvidencePtr(in.FirstPassedAt)
	out.StateSince, out.BlockersSince = obsDeploymentEvidencePtr(in.StateSince), obsDeploymentEvidencePtr(in.BlockersSince)
	return &out
}
