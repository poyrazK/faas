package state

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectCanaryPolicyState is an internal checkpoint, not a public admission
// contract. Callers must persist the returned checkpoint and its graph/config
// effects in the same transaction before using it to route production work.
type ProjectCanaryPolicyState struct {
	Stabilizing        bool                       `json:"stabilizing,omitempty"`
	Control            *api.ProjectReleaseControl `json:"control,omitempty"`
	ID                 string                     `json:"id"`
	BaselineReleaseID  string                     `json:"baseline_release_id"`
	CandidateReleaseID string                     `json:"candidate_release_id"`
	WorkerRequired     bool                       `json:"worker_required"`
	Percent            int                        `json:"percent"`
	Status             string                     `json:"status"`
	Generation         uint64                     `json:"generation"`
	StageStartedAt     time.Time                  `json:"stage_started_at"`
	Deadline           time.Time                  `json:"deadline"`
	CheckedAt          *time.Time                 `json:"checked_at,omitempty"`
	HealthyRounds      int                        `json:"healthy_rounds"`
	RegressedRounds    int                        `json:"regressed_rounds"`
}

// ProjectCanaryObservation uses separate HTTP and worker verdicts so HTTP
// success cannot promote a worker without actual delivery evidence. Generation
// and CheckedAt are compare-and-swap tokens, including across stage boundaries.
type ProjectCanaryObservation struct {
	Generation        uint64
	PreviousCheckedAt *time.Time
	EvidenceStartedAt time.Time
	EvidenceEndedAt   time.Time
	BaselineCheckedAt time.Time
	HTTPVerdict       string
	WorkerVerdict     string
	BaselineHealthy   bool
}

// ProjectCanaryEffect describes the transaction required by a decision.
// Before final promotion the baseline remains active and the global config
// head stays unchanged. Abort stops new candidate admissions in either case;
// already pinned work must retain its graph rather than be silently retargeted.
type ProjectCanaryEffect string

const (
	ProjectCanaryNoEffect      ProjectCanaryEffect = ""
	ProjectCanaryPromote       ProjectCanaryEffect = "promote_cohort"
	ProjectCanaryActivate      ProjectCanaryEffect = "activate_candidate_and_config"
	ProjectCanaryAbort         ProjectCanaryEffect = "stop_candidate_admission"
	ProjectCanaryRestore       ProjectCanaryEffect = "restore_baseline_and_config"
	ProjectCanaryComplete      ProjectCanaryEffect = "complete"
	ProjectCanaryNeedsOperator ProjectCanaryEffect = "needs_operator"
)

var errInvalidProjectCanary = errors.New("invalid project canary checkpoint")

func NewProjectCanaryPolicy(id, baseline, candidate string, workerRequired bool, now time.Time) (ProjectCanaryPolicyState, error) {
	s := ProjectCanaryPolicyState{
		ID: id, BaselineReleaseID: baseline, CandidateReleaseID: candidate,
		WorkerRequired: workerRequired, Percent: api.ProjectCanaryInitialPercent,
		Status: "running", Generation: 1, StageStartedAt: now,
		Deadline: now.Add(api.ProjectReleaseVerificationTimeout),
	}
	return s, s.validate()
}

func (s ProjectCanaryPolicyState) validate() error {
	if c := s.Control; c != nil {
		if !api.ValidDeploymentAttemptID(c.RequestID) || c.ExpectedGeneration == 0 || c.ExpectedGeneration >= s.Generation || c.RequestedAt.IsZero() || (c.Action != "abort" && c.Action != "recover") || (c.Status != "pending" && c.Status != "restored" && c.Status != "blocked") {
			return errInvalidProjectCanary
		}
		if c.Status == "pending" && (s.Status != "running" || s.Percent != api.ProjectCanaryFinalPercent) {
			return errInvalidProjectCanary
		}
	}

	if s.ID == "" || s.BaselineReleaseID == "" || s.CandidateReleaseID == "" || s.BaselineReleaseID == s.CandidateReleaseID || s.Generation == 0 || s.StageStartedAt.IsZero() || !s.Deadline.Equal(s.StageStartedAt.Add(api.ProjectReleaseVerificationTimeout)) {
		return errInvalidProjectCanary
	}
	if s.Percent != api.ProjectCanaryInitialPercent && s.Percent != api.ProjectCanaryMiddlePercent && s.Percent != api.ProjectCanaryFinalPercent {
		return errInvalidProjectCanary
	}
	switch s.Status {
	case "running", "needs_operator":
	case "aborted":
		if s.Percent == api.ProjectCanaryFinalPercent {
			return errInvalidProjectCanary
		}
	case "rollback_required":
		if s.Percent != api.ProjectCanaryFinalPercent {
			return errInvalidProjectCanary
		}
	case "rolled_back":
		if s.Percent != api.ProjectCanaryFinalPercent {
			return errInvalidProjectCanary
		}
	case "complete":
		if s.Percent != api.ProjectCanaryFinalPercent || !s.Stabilizing && (s.HealthyRounds != api.ProjectReleaseVerificationRounds || s.CheckedAt == nil) {
			return errInvalidProjectCanary
		}
	default:
		return errInvalidProjectCanary
	}
	if s.HealthyRounds < 0 || s.RegressedRounds < 0 || s.HealthyRounds > api.ProjectReleaseVerificationRounds || s.RegressedRounds > api.ProjectReleaseVerificationRounds || s.HealthyRounds > 0 && s.RegressedRounds > 0 {
		return errInvalidProjectCanary
	}
	if s.Status == "running" && (s.HealthyRounds == api.ProjectReleaseVerificationRounds || s.RegressedRounds == api.ProjectReleaseVerificationRounds) {
		return errInvalidProjectCanary
	}
	if s.CheckedAt == nil {
		if s.HealthyRounds != 0 || s.RegressedRounds != 0 {
			return errInvalidProjectCanary
		}
	} else if s.CheckedAt.Before(s.StageStartedAt) || !s.CheckedAt.Before(s.Deadline) {
		return errInvalidProjectCanary
	}
	return nil
}

// DecideProjectCanary is pure: effects must be committed by the graph owner.
// Deadline handling requires a fresh, matching observation even after restart;
// missing or unhealthy baseline evidence never authorizes restoring it.
func DecideProjectCanary(s ProjectCanaryPolicyState, o ProjectCanaryObservation, now time.Time) (ProjectCanaryPolicyState, ProjectCanaryEffect, error) {
	if err := s.validate(); err != nil {
		return s, ProjectCanaryNoEffect, err
	}
	if s.Control != nil || s.Status != "running" || o.Generation != s.Generation || !sameCanaryCheckpoint(s.CheckedAt, o.PreviousCheckedAt) || now.Before(s.StageStartedAt) || s.CheckedAt != nil && now.Before(*s.CheckedAt) {
		return s, ProjectCanaryNoEffect, nil
	}
	baselineHealthy := o.BaselineHealthy && !o.BaselineCheckedAt.After(now) && now.Sub(o.BaselineCheckedAt) < api.ProjectReleaseVerificationInterval
	if !now.Before(s.Deadline) {
		return stopProjectCanary(s, baselineHealthy)
	}
	// A stage needs a complete, disjoint observation interval after activation.
	last := s.StageStartedAt
	if s.CheckedAt != nil {
		last = *s.CheckedAt
	}
	if !o.EvidenceStartedAt.Equal(last) || o.EvidenceEndedAt.Sub(last) < api.ProjectReleaseVerificationInterval || o.EvidenceEndedAt.After(now) || now.Sub(o.EvidenceEndedAt) >= api.ProjectReleaseVerificationInterval {
		return s, ProjectCanaryNoEffect, nil
	}
	checkedAt := o.EvidenceEndedAt
	s.CheckedAt = &checkedAt
	s.Generation++
	verdict := o.HTTPVerdict
	if !baselineHealthy {
		verdict = "unknown"
	} else if s.WorkerRequired {
		switch o.WorkerVerdict {
		case "regressed":
			if verdict == "healthy" {
				verdict = "regressed"
			}
		case "healthy":
		default:
			if verdict == "healthy" {
				verdict = "unknown"
			}
		}
	}
	switch verdict {
	case "healthy":
		s.HealthyRounds++
		s.RegressedRounds = 0
	case "regressed":
		s.RegressedRounds++
		s.HealthyRounds = 0
	default:
		s.HealthyRounds, s.RegressedRounds = 0, 0
	}
	if s.RegressedRounds == api.ProjectReleaseVerificationRounds {
		return stopProjectCanary(s, true)
	}
	if s.HealthyRounds < api.ProjectReleaseVerificationRounds {
		return s, ProjectCanaryNoEffect, nil
	}
	if s.Percent == api.ProjectCanaryFinalPercent {
		s.Status = "complete"
		return s, ProjectCanaryComplete, nil
	}
	effect := ProjectCanaryPromote
	if s.Percent == api.ProjectCanaryInitialPercent {
		s.Percent = api.ProjectCanaryMiddlePercent
	} else {
		s.Percent = api.ProjectCanaryFinalPercent
		effect = ProjectCanaryActivate
	}
	s.StageStartedAt, s.Deadline = now, now.Add(api.ProjectReleaseVerificationTimeout)
	s.CheckedAt = nil
	s.HealthyRounds, s.RegressedRounds = 0, 0
	return s, effect, nil
}

func stopProjectCanary(s ProjectCanaryPolicyState, baselineHealthy bool) (ProjectCanaryPolicyState, ProjectCanaryEffect, error) {
	s.Generation++
	if !baselineHealthy {
		s.Status = "needs_operator"
		return s, ProjectCanaryNeedsOperator, nil
	}
	if s.Percent == api.ProjectCanaryFinalPercent {
		s.Status = "rollback_required"
		return s, ProjectCanaryRestore, nil
	}
	s.Status = "aborted"
	return s, ProjectCanaryAbort, nil
}

func sameCanaryCheckpoint(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

// SelectProjectCanaryRelease chooses only new work. requestKey must be generated
// at trusted ingress; jobs use their durable replay-root identity. Persist the
// result on first admission and reuse that pin for retries/replays: rerunning
// selection after a promotion is intentionally not a substitute for a pin.
func SelectProjectCanaryRelease(s ProjectCanaryPolicyState, requestKey string, now time.Time) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	if requestKey == "" || now.Before(s.StageStartedAt) {
		return "", errInvalidProjectCanary
	}
	if s.Status == "complete" {
		return s.CandidateReleaseID, nil
	}
	if s.Control != nil && s.Control.Status == "pending" || s.Status != "running" || !now.Before(s.Deadline) {
		return "", errors.New("project canary admission stopped")
	}
	// JSON framing prevents collisions between rollout/key pairs. The release
	// IDs participate so a checkpoint cannot reuse another graph's cohort salt.
	key, _ := json.Marshal([]string{s.ID, s.BaselineReleaseID, s.CandidateReleaseID, requestKey})
	hash := sha256.Sum256(key)
	bucket := binary.BigEndian.Uint64(hash[:8]) % api.ProjectCanaryBucketCount
	if bucket < uint64(s.Percent*api.ProjectCanaryBucketCount/api.ProjectCanaryFinalPercent) {
		return s.CandidateReleaseID, nil
	}
	return s.BaselineReleaseID, nil
}
