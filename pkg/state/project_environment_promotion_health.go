package state

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type PromotionHealthJob struct{ PromotionID, AccountID, ProjectSlug, ToEnvironment string }

// Observations are internal, attributed samples. Generation makes retries and
// competing reconcilers unable to count the same checkpoint twice.
type PromotionHealthObservation struct {
	LeaseToken string
	Generation int64
	Evidence   []api.ProjectReleaseProbeEvidence
	Action     string
}

type ProjectPromotionHealthStore interface {
	ClaimProjectEnvironmentPromotionHealth(context.Context, string, string, string) (bool, error)
	ReleaseProjectEnvironmentPromotionHealth(context.Context, string, string) error
	ProjectEnvironmentPromotionHealth(context.Context, string, string) (*api.ProjectPromotionHealth, error)
	PendingProjectEnvironmentPromotionHealth(context.Context) ([]PromotionHealthJob, error)
	AdvanceProjectEnvironmentPromotionHealth(context.Context, string, string, PromotionHealthObservation) (*api.ProjectPromotionHealth, error)
}

func ValidatePromotionHealthPolicy(policy *api.ProjectPromotionHealthPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.Migration != nil && (policy.Migration.AppID == "" || !api.ValidAppSlug(policy.Migration.Slug) || !validSecretRevision(policy.Migration.CommandHash) || policy.Migration.Compatibility != "backward-compatible") {
		return ErrInvalidArgument
	}
	if policy.Worker != nil && len(policy.Workers) > 0 || len(policy.Workers) == 1 || len(policy.Workers) > api.ProjectPromotionMaxWorkers {
		return ErrInvalidArgument
	}
	previousWorker := ""
	bindings := map[string]bool{}
	for _, w := range api.ProjectPromotionWorkers(policy) {
		if w == nil || (policy.Migration != nil && policy.Migration.AppID == w.AppID || w.AppID == "" || !api.ValidAppSlug(w.Slug) || w.BindingID == "" || !validSecretRevision(w.BindingHash) || w.PreviousDeploymentID == "" || w.DrainSeconds <= 0 || w.DrainSeconds > api.ProjectReleaseWorkerMaxDrainSeconds || w.Status != "building" || w.DeploymentID != "" || w.InstanceID != "") {
			return ErrInvalidArgument
		}
		if w.AppID <= previousWorker || bindings[w.BindingID] {
			return ErrInvalidArgument
		}
		previousWorker = w.AppID
		bindings[w.BindingID] = true
	}
	if len(policy.Probes) == 0 || len(policy.Probes) > api.ProjectReleaseVerificationMaxProbes {
		return ErrInvalidArgument
	}
	previous := ""
	for _, p := range policy.Probes {
		key := p.AppID + p.Method + p.Path
		u, err := url.Parse(p.Path)
		if p.AppID == "" || !api.ValidAppSlug(p.Slug) || p.Method != "GET" && p.Method != "HEAD" || err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(p.Path, "/") || strings.HasPrefix(p.Path, "//") || key <= previous {
			return ErrInvalidArgument
		}
		previous = key
	}
	return nil
}

func newPromotionHealth(p ProjectEnvironmentPromotion, now time.Time) (*api.ProjectPromotionHealth, error) {
	if p.HealthPolicy == nil {
		return nil, nil
	}
	if err := ValidatePromotionHealthPolicy(p.HealthPolicy); err != nil {
		return nil, err
	}
	if !p.SyncConfig || p.ReleaseConfigurationHash == "" || !p.ReleaseGraphMode || p.SourceReleaseSetID == "" || p.PreviousTargetReleaseSetID == "" || time.Duration(p.ReleaseTTLSeconds)*time.Second < api.ProjectPromotionHealthMinRetention {
		return nil, ErrInvalidArgument
	}
	h := &api.ProjectPromotionHealth{Policy: *p.HealthPolicy, Phase: "preflight", Generation: 1, StartedAt: now, Deadline: now.Add(api.ProjectReleaseVerificationTimeout), Verdict: "unknown"}
	if p.HealthPolicy.Worker != nil {
		h.Worker = copyPromotionWorker(p.HealthPolicy.Worker)
		h.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
	}
	for _, worker := range p.HealthPolicy.Workers {
		h.Workers = append(h.Workers, api.ProjectPromotionWorkerHealth{Worker: copyPromotionWorker(worker), Health: &api.ProjectWorkerHealth{Status: "insufficient_evidence"}})
	}
	if p.HealthPolicy.Migration != nil {
		h.Phase = "migrating"
		h.Deadline = now.Add(api.ProjectPromotionMigrationTimeout)
		h.Migration = promotionMigrationProgress(p.HealthPolicy.Migration)
	}
	return clonePromotionHealth(h), nil
}

func clonePromotionHealth(h *api.ProjectPromotionHealth) *api.ProjectPromotionHealth {
	if h == nil {
		return nil
	}
	copy := *h
	if h.Admission != nil {
		a := *h.Admission
		if a.StartedAt != nil {
			at := *a.StartedAt
			a.StartedAt = &at
		}
		copy.Admission = &a
	}
	copy.Control = clonePromotionControl(h.Control)
	copy.Policy.Worker = copyPromotionWorker(h.Policy.Worker)
	copy.Policy.Workers = copyPromotionWorkers(h.Policy.Workers)
	copy.Workers = nil
	for _, p := range h.Workers {
		var health *api.ProjectWorkerHealth
		if p.Health != nil {
			v := *p.Health
			health = &v
		}
		copy.Workers = append(copy.Workers, api.ProjectPromotionWorkerHealth{Worker: copyPromotionWorker(p.Worker), Health: health})
	}
	copy.Worker = copyPromotionWorker(h.Worker)
	if h.WorkerHealth != nil {
		health := *h.WorkerHealth
		copy.WorkerHealth = &health
	}
	if h.Policy.Migration != nil {
		migration := *h.Policy.Migration
		copy.Policy.Migration = &migration
	}
	if h.Migration != nil {
		progress := *h.Migration
		if h.Migration.ExitCode != nil {
			code := *h.Migration.ExitCode
			progress.ExitCode = &code
		}
		copy.Migration = &progress
	}
	copy.Policy.Probes = append([]api.ProjectReleaseProbe(nil), h.Policy.Probes...)
	copy.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), h.Evidence...)
	if h.CheckedAt != nil {
		stamp := *h.CheckedAt
		copy.CheckedAt = &stamp
	}
	return &copy
}

func promotionHealthPending(phase string) bool {
	return phase == "queued" || phase == "migrating" || phase == "preflight" || phase == "cutover" || phase == "verifying" || phase == "finalizing" || phase == "rolling_back" || phase == "restoring"
}

func promotionHealthBaselineHealthy(h *api.ProjectPromotionHealth, now time.Time) bool {
	if !promotionWorkerBaselineHealthy(h) {
		return false
	}
	if h.CheckedAt == nil || now.Before(*h.CheckedAt) || now.Sub(*h.CheckedAt) > api.ProjectMigrationBaselineEvidenceMaxAge || len(h.Evidence) != len(h.Policy.Probes) {
		return false
	}
	for i, e := range h.Evidence {
		if e.Probe != h.Policy.Probes[i] || e.StableRequests != api.RouteHealthProbeRequestsPerMinute || e.StableErrors != 0 || e.Unauthenticated != 0 || e.UnexpectedResponses != 0 {
			return false
		}
	}
	return true
}

// decidePromotionHealth uses persisted server timestamps. Unknown evidence
// resets consecutive counts and never authorizes cutover or success.
func decidePromotionHealth(h *api.ProjectPromotionHealth, p ProjectEnvironmentPromotion, o PromotionHealthObservation, now time.Time) (*api.ProjectPromotionHealth, error) {
	if h == nil || h.Generation != o.Generation {
		return h, ErrConflict
	}
	if h.Phase == "queued" {
		if h.Admission == nil {
			return h, ErrConflict
		}
		return queuedPromotionDecision(h, o.Action, now), nil
	}
	if !promotionHealthPending(h.Phase) {
		return h, nil
	}
	if h.Control != nil && h.Control.Status == "pending" {
		return decidePromotionRestoration(h, p, o, now)
	}
	next := clonePromotionHealth(h)
	if o.Action == "complete" && !now.Before(h.Deadline) {
		o.Action = ""
	}
	switch o.Action {
	case "conflict":
		next.Phase, next.Message = "conflicted", "Destination intent changed; this promotion will not overwrite it."
		if h.Phase == "migrating" && next.Migration != nil && next.Migration.TaskID != "" && next.Migration.Status != "succeeded" {
			next.Migration.Status = "uncertain"
			next.Message += " Inspect the migration task and database before retrying."
		}
	case "fail":
		next.Phase, next.Message = "failed", "Promotion record verification failed before cutover."
		if p.TargetReleaseSetID != "" {
			next.Phase, next.Message = "rolling_back", "Promotion record verification failed; restoring the previous release."
			if !promotionHealthBaselineHealthy(h, now) {
				next.Phase, next.Message = "rollback_failed", "Release record verification failed without a fresh healthy baseline; operator action is required."
			}
		}
	case "complete":
		if h.Phase != "finalizing" || h.Verdict != "healthy" || h.HealthyRounds < api.ProjectReleaseVerificationRounds || !promotionHealthBaselineHealthy(h, now) {
			return h, ErrConflict
		}
		next.Phase, next.Message = "succeeded", "Destination health and release records verified."
	case "rolled_back":
		if h.Phase != "rolling_back" || p.RollbackStatus != "rolled_back" {
			return h, ErrConflict
		}
		next.Phase = "failed"
	case "rollback_failed":
		if h.Phase != "rolling_back" {
			return h, ErrConflict
		}
		next.Phase, next.Message = "rollback_failed", "Automatic restoration could not be confirmed; review the destination before retrying."
	case "":
		if h.Phase != "migrating" && h.Phase != "preflight" && h.Phase != "verifying" && h.Phase != "cutover" && h.Phase != "finalizing" {
			return h, nil
		}
		if !now.Before(h.Deadline) {
			next.Phase, next.Message = "failed", "Destination health deadline expired before cutover; the previous release remains active."
			if p.TargetReleaseSetID != "" {
				next.Phase, next.Message = "rolling_back", "Destination health deadline expired; restoring the qualified previous release."
				if !promotionHealthBaselineHealthy(h, now) {
					next.Phase, next.Message = "rollback_failed", "Health deadline expired without a fresh healthy baseline; operator action is required."
				}
			}
			if h.Phase == "migrating" && next.Migration != nil && next.Migration.TaskID != "" {
				next.Migration.Status = "uncertain"
				next.Message = "Migration deadline expired; inspect the task and database before another attempt. The destination release was not switched."
			}
			break
		}
		if h.Phase == "migrating" {
			return h, nil
		}
		if h.Phase == "cutover" || h.Phase == "finalizing" {
			return h, nil
		}
		if h.CheckedAt != nil && now.Sub(*h.CheckedAt) < api.ProjectReleaseVerificationInterval {
			return h, nil
		}
		next.CheckedAt = &now
		next.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), o.Evidence...)
		next.Verdict = ProjectReleaseProbeVerdict(h.Policy.Probes, o.Evidence)
		for _, e := range o.Evidence {
			if e.CandidateRequests > api.RouteHealthProbeRequestsPerMinute || e.StableRequests > api.RouteHealthProbeRequestsPerMinute || e.StableErrors != 0 {
				next.Verdict = "unknown"
			}
		}
		next.Verdict = promotionWorkerVerdict(next, next.Verdict)
		switch next.Verdict {
		case "healthy":
			next.HealthyRounds++
			next.RegressedRounds = 0
		case "regressed":
			next.RegressedRounds++
			next.HealthyRounds = 0
		default:
			next.HealthyRounds, next.RegressedRounds = 0, 0
		}
		if next.HealthyRounds >= api.ProjectReleaseVerificationRounds {
			next.Phase = "cutover"
			if h.Phase == "verifying" {
				next.Phase = "finalizing"
			}
		}
		if next.RegressedRounds >= api.ProjectReleaseVerificationRounds {
			next.Phase, next.Message = "failed", "Candidates repeatedly failed destination probes; the previous release remains active."
			if h.Phase == "verifying" {
				next.Phase, next.Message = "rolling_back", "Repeated destination regressions with a healthy baseline; restoring the previous release."
			}
		}
	default:
		return h, ErrInvalidArgument
	}
	next.Generation++
	return next, nil
}

func promotionHealthAfterCutover(h *api.ProjectPromotionHealth, now time.Time) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	next.Phase, next.StartedAt, next.Deadline = "verifying", now, now.Add(api.ProjectReleaseVerificationTimeout)
	next.CheckedAt = nil
	next.HealthyRounds, next.RegressedRounds = 0, 0
	next.Verdict, next.Evidence = "unknown", nil
	for _, one := range promotionWorkerHealthViews(next) {
		if one.WorkerHealth != nil {
			one.WorkerHealth.BaselinePendingJobs = one.WorkerHealth.PendingJobs
			one.WorkerHealth.BaselineOldestPendingAgeMS = one.WorkerHealth.OldestPendingAgeMS
			one.WorkerHealth.Candidate = api.ProjectWorkerJobSample{}
			one.WorkerHealth.Status = "insufficient_evidence"
		}
	}
	next.Generation++
	return next
}

func promotionHealthResult(h *api.ProjectPromotionHealth) (status, verification, message string) {
	status, verification = "running", "verifying"
	if !promotionHealthPending(h.Phase) {
		status, verification = "failed", "failed"
		if h.Phase == "succeeded" {
			status, verification = "succeeded", "verified"
		}
	}
	if status == "failed" {
		message = h.Message
	}
	return
}

func encodePromotionHealth(h *api.ProjectPromotionHealth) ([]byte, error) {
	raw, err := json.Marshal(h)
	if err != nil || len(raw) > api.ProjectPromotionHealthMaxBytes {
		return nil, ErrInvalidArgument
	}
	return raw, nil
}
