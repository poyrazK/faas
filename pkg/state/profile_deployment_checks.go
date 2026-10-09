package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
)

var ErrProfileCheckLease = errors.New("deployment profiling check lease is stale")

var (
	_ ProfileDeploymentCheckStore = (*MemStore)(nil)
	_ ProfileDeploymentCheckStore = (*PgStore)(nil)
	_ ProfileCanaryCheckStore     = (*MemStore)(nil)
	_ ProfileCanaryCheckStore     = (*PgStore)(nil)
	_ ProfileRequestCountReader   = (*PgStore)(nil)
)

type ProfileDeploymentCheckWork struct {
	Check            api.ProfileDeploymentCheck
	AccountID, Token string
}

type ProfileCanaryCheckKey struct {
	DeploymentID        string
	CanaryStep          int
	CanaryStepStartedAt time.Time
	PolicyRevision      int64
}

type ProfileCanaryCheckWork struct {
	Check            api.CanaryProfileSignal
	AppID            string
	AccountID, Token string
}

type ProfileDeploymentCheckStore interface {
	GetProfileDeploymentPolicy(context.Context, string, string) (api.ProfileDeploymentPolicy, error)
	SaveProfileDeploymentPolicy(context.Context, string, string, api.SaveProfileDeploymentPolicyRequest) (api.ProfileDeploymentPolicy, error)
	ListProfileDeploymentChecks(context.Context, string, string) ([]api.ProfileDeploymentCheck, error)
	GetProfileDeploymentCheck(context.Context, string, string, string) (api.ProfileDeploymentCheck, error)
	DiscoverProfileDeploymentChecks(context.Context, time.Time) (int, error)
	MaintainProfileDeploymentChecks(context.Context, time.Time) error
	ClaimProfileDeploymentCheck(context.Context, time.Time) (ProfileDeploymentCheckWork, error)
	FinishProfileDeploymentCheck(context.Context, ProfileDeploymentCheckWork, api.ProfileRegressionAssessment, bool, time.Time) (api.ProfileDeploymentCheck, error)
}

// ProfileCanaryCheckStore retains a bounded receipt for each deployment stage
// instance and policy revision. Opt-in gates collect distinct comparison windows.
type ProfileCanaryCheckStore interface {
	GetProfileCanaryCheck(context.Context, string, string, ProfileCanaryCheckKey) (api.CanaryProfileSignal, error)
	GetLatestProfileCanaryCheck(context.Context, string, string, string, int64) (api.CanaryProfileSignal, error)
	ListProfileCanaryChecks(context.Context, string, string, string, int, string) (api.ProfileCanaryHistoryPage, error)
	DiscoverProfileCanaryChecks(context.Context, time.Time) (int, error)
	MaintainProfileCanaryChecks(context.Context, time.Time) error
	ClaimProfileCanaryCheck(context.Context, time.Time) (ProfileCanaryCheckWork, error)
	FinishProfileCanaryCheck(context.Context, ProfileCanaryCheckWork, api.ProfileRegressionAssessment, bool, time.Time) (api.CanaryProfileSignal, error)
}

func DefaultProfileDeploymentPolicy(appID, runtime string) api.ProfileDeploymentPolicy {
	if runtime == "" {
		runtime = "custom"
	}
	return api.ProfileDeploymentPolicy{AppID: appID, Config: api.ProfileDeploymentPolicyConfig{Runtime: runtime, WindowSeconds: api.ProfileAutoDefaultWindowSeconds, WarmupSeconds: api.ProfileAutoDefaultWarmupSeconds, Options: api.DefaultProfileRegressionOptions()}}
}

func ValidateProfileDeploymentPolicy(req api.SaveProfileDeploymentPolicyRequest) error {
	c := req.Config
	if err := validateProfileCanaryGatePolicy(c); err != nil {
		return err
	}
	if err := validatePeriodicProfilePolicy(c); err != nil {
		return err
	}
	if c.NotifyRouteRegressions && (!c.Enabled || len(c.Options.Routes) == 0) {
		return errors.New("route notifications require enabled automatic checks and explicit advisory routes")
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 0 || *req.ExpectedRevision >= api.ProfileInvestigationMaxRevision {
		return errors.New("expected_revision must name the current policy revision; use 0 for the initial policy")
	}
	if !profiling.ValidRuntime(c.Runtime) || c.Runtime == "" || c.WindowSeconds < api.ProfileAutoMinWindowSeconds || c.WindowSeconds > api.ProfileAutoMaxWindowSeconds || c.WarmupSeconds < 0 || c.WarmupSeconds > api.ProfileAutoMaxWarmupSeconds {
		return fmt.Errorf("require runtime, window_seconds %d–%d and warmup_seconds 0–%d", api.ProfileAutoMinWindowSeconds, api.ProfileAutoMaxWindowSeconds, api.ProfileAutoMaxWarmupSeconds)
	}
	return profiling.ValidateRegressionOptions(c.Options)
}

func newProfileDeploymentCheck(d Deployment, baselineID string, policy api.ProfileDeploymentPolicy, now time.Time) api.ProfileDeploymentCheck {
	window := time.Duration(policy.Config.WindowSeconds) * time.Second
	start := d.RolloutCompletedAt.Add(time.Duration(policy.Config.WarmupSeconds) * time.Second)
	due := start.Add(window).Add(api.ProfileAutoIngestionGrace)
	out := api.ProfileDeploymentCheck{DeploymentID: d.ID, AppID: d.AppID, Scope: normalizedDeploymentScope(d.Scope), PolicyRevision: policy.Revision, Config: policy.Config, Status: "queued", Reason: "Waiting for the capture window and profile ingestion.", CreatedAt: now, NextAttemptAt: &due, Candidate: api.ProfileQuery{DeploymentID: d.ID, Runtime: policy.Config.Runtime, Start: start, End: start.Add(window)}}
	if baselineID != "" {
		out.Baseline = &api.ProfileQuery{DeploymentID: baselineID, Runtime: policy.Config.Runtime, Start: d.CreatedAt.Add(-window), End: d.CreatedAt}
	}
	return out
}

func copyProfileDeploymentCheck(in api.ProfileDeploymentCheck) api.ProfileDeploymentCheck {
	body, _ := json.Marshal(in)
	var out api.ProfileDeploymentCheck
	_ = json.Unmarshal(body, &out)
	return out
}

func profileCheckInvestigation(check api.ProfileDeploymentCheck, assessment api.ProfileRegressionAssessment) (api.SaveProfileInvestigationRequest, error) {
	if check.Baseline == nil {
		return api.SaveProfileInvestigationRequest{}, ErrNotFound
	}
	zero := int64(0)
	input := api.ProfileInvestigationInput{Title: "Automatic CPU check: " + check.DeploymentID, Findings: assessment.Reason, Notes: "Automatically compared fixed equal windows for the baseline and candidate deployments. Traffic, replicas and CPU allocation can affect this comparison.", Baseline: *check.Baseline, Candidate: check.Candidate}
	for _, evidence := range assessment.Evidence {
		if evidence.Kind != "call_path" {
			continue
		}
		bytes := 0
		for _, f := range evidence.Frames {
			bytes += len(f.Name) + len(f.File)
		}
		if bytes <= api.ProfileInvestigationMaxPathBytes {
			input.SelectedPath = &api.ProfileCallPath{View: "comparison", Frames: evidence.Frames}
			break
		}
	}
	req := api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: input}
	return req, validateProfileInvestigationAt(req, assessment.CheckedAt)
}
