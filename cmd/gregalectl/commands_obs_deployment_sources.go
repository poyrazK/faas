package main

import (
	"net/http"
	"time"
)

type obsDeploymentReadTiming struct {
	started, completed time.Time
	seconds            float64
}

type obsDeploymentSources struct {
	ServingHealth      obsDeploymentSourceSummary `json:"serving_health"`
	NotificationHealth obsDeploymentSourceSummary `json:"notification_health"`
}

type obsDeploymentSourceSummary struct {
	State               string                      `json:"state"`
	Reason              string                      `json:"reason,omitempty"`
	HTTPStatus          *int                        `json:"http_status,omitempty"`
	Reads               int                         `json:"reads"`
	FailedReads         int                         `json:"failed_reads"`
	CancelledReads      int                         `json:"cancelled_reads"`
	ConsecutiveFailures int                         `json:"consecutive_failures"`
	LastReadAt          *time.Time                  `json:"last_read_at"`
	LastReadStartedAt   *time.Time                  `json:"last_read_started_at,omitempty"`
	LastReadCompletedAt *time.Time                  `json:"last_read_completed_at,omitempty"`
	LastReadSeconds     *float64                    `json:"last_read_seconds,omitempty"`
	LastSuccessAt       *time.Time                  `json:"last_success_at"`
	FirstFailureAt      *time.Time                  `json:"first_failure_at"`
	LastFailure         *obsDeploymentSourceFailure `json:"last_failure,omitempty"`
	LastRecoveryAt      *time.Time                  `json:"last_recovery_at"`
}

type obsDeploymentSourceFailure struct {
	At         time.Time `json:"at"`
	Reason     string    `json:"reason"`
	HTTPStatus *int      `json:"http_status,omitempty"`
}

type obsDeploymentSourceRecorder struct {
	sources obsDeploymentSources
}

func obsDeploymentReadTime(started, completed time.Time) *obsDeploymentReadTiming {
	return &obsDeploymentReadTiming{started: started.UTC(), completed: completed.UTC(), seconds: max(0, completed.Sub(started).Seconds())}
}

func obsDeploymentSourceOutcome(err error, valid bool, timing *obsDeploymentReadTiming) (string, string, *int) {
	if err != nil {
		reason, status := obsFetchFailureDetails(err)
		if reason == "cancelled" {
			return "cancelled", reason, status
		}
		return "failed", reason, status
	}
	var status *int
	if timing != nil {
		ok := http.StatusOK // The shared reader returns nil only for a decoded HTTP 200.
		status = &ok
	}
	if !valid {
		return "failed", "invalid_response", status
	}
	return "succeeded", "", status
}

func obsDeploymentSourceFailureCheck(check obsDeploymentCheck, err error, timing *obsDeploymentReadTiming) obsDeploymentCheck {
	_, reason, status := obsDeploymentSourceOutcome(err, false, timing)
	check.Reason, check.SourceHTTPStatus = "source_"+reason, status
	return check
}

func (r *obsDeploymentSourceRecorder) observe(snapshot obsDeploymentSnapshot, servingValid, notificationValid bool, at time.Time) *obsDeploymentSources {
	r.sources.ServingHealth.observe(snapshot.serving.err, servingValid, snapshot.serving.timing, at)
	r.sources.NotificationHealth.observe(snapshot.notificationErr, notificationValid, snapshot.notificationTiming, at)
	return r.snapshot()
}

func (s *obsDeploymentSourceSummary) observe(err error, valid bool, timing *obsDeploymentReadTiming, at time.Time) {
	s.State, s.Reason, s.HTTPStatus = obsDeploymentSourceOutcome(err, valid, timing)
	s.Reads++
	s.LastReadStartedAt, s.LastReadCompletedAt, s.LastReadSeconds = nil, nil, nil
	if timing != nil {
		s.LastReadStartedAt, s.LastReadCompletedAt = obsDeploymentEvidencePtr(&timing.started), obsDeploymentEvidencePtr(&timing.completed)
		s.LastReadSeconds = obsDeploymentEvidencePtr(&timing.seconds)
		at = timing.completed
	}
	observed := at.UTC()
	s.LastReadAt = &observed
	switch s.State {
	case "failed":
		s.FailedReads++
		s.ConsecutiveFailures++
		if s.FirstFailureAt == nil {
			s.FirstFailureAt = &observed
		}
		s.LastFailure = &obsDeploymentSourceFailure{At: observed, Reason: s.Reason, HTTPStatus: obsDeploymentEvidencePtr(s.HTTPStatus)}
	case "cancelled":
		s.CancelledReads++
	case "succeeded":
		if s.ConsecutiveFailures > 0 {
			s.LastRecoveryAt = &observed
		}
		s.LastSuccessAt, s.ConsecutiveFailures = &observed, 0
	}
}

func (r *obsDeploymentSourceRecorder) snapshot() *obsDeploymentSources {
	out := r.sources
	if out.ServingHealth.State == "" {
		out.ServingHealth.State = "not_read"
	}
	if out.NotificationHealth.State == "" {
		out.NotificationHealth.State = "not_read"
	}
	out.ServingHealth.HTTPStatus = obsDeploymentEvidencePtr(out.ServingHealth.HTTPStatus)
	out.NotificationHealth.HTTPStatus = obsDeploymentEvidencePtr(out.NotificationHealth.HTTPStatus)
	if out.ServingHealth.LastFailure != nil {
		failure := *out.ServingHealth.LastFailure
		failure.HTTPStatus = obsDeploymentEvidencePtr(failure.HTTPStatus)
		out.ServingHealth.LastFailure = &failure
	}
	if out.NotificationHealth.LastFailure != nil {
		failure := *out.NotificationHealth.LastFailure
		failure.HTTPStatus = obsDeploymentEvidencePtr(failure.HTTPStatus)
		out.NotificationHealth.LastFailure = &failure
	}
	return &out
}
