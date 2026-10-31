// Package conditions derives resource conditions from API evidence
// (ADR-20261030151810). Every function is pure: conditions are computed when
// a status is read and never stored, so nothing here reads state or writes it.
package conditions

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// App health values, as pkg/apphealth reports them. They are repeated here
// so the mapping depends only on the API response.
const (
	healthHealthy   = "healthy"
	healthDegraded  = "degraded"
	healthUnhealthy = "unhealthy"
	healthUnknown   = "unknown"

	checkPass          = "pass"
	checkWarning       = "warning"
	checkFail          = "fail"
	checkUnknown       = "unknown"
	checkNotApplicable = "not_applicable"

	checkLatestDeployment = "latest_deployment"
	checkRequests         = "requests"
)

// Deployment statuses and rollout states, as the deployments API reports them.
const (
	deploymentLive       = "live"
	deploymentFailed     = "failed"
	deploymentCancelled  = "cancelled"
	deploymentSuperseded = "superseded"

	rolloutComplete = "complete"
	rolloutAborted  = "aborted"
)

// App returns an app's status from its health assessment.
func App(slug string, health api.AppHealthResponse, observedAt time.Time) api.ResourceStatus {
	return api.ResourceStatus{
		Kind: api.ResourceKindApp, Name: slug, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{appReady(health), appProgressing(health), appDegraded(health)},
	}
}

func appReady(h api.AppHealthResponse) api.Condition {
	c := api.Condition{Type: api.ConditionReady}
	latest, hasLatest := check(h, checkLatestDeployment)
	// Request telemetry says how traffic went, not whether the app can serve,
	// and plans without per-app metrics never have it: Ready leaves it to
	// Degraded.
	worst, _ := worstCheck(h, checkRequests)
	switch {
	case h.Phase == "unsupported":
		c.Status, c.Reason = api.ConditionUnknown, api.ConditionReasonNotAssessed
		c.Message = "Readiness is assessed for apps that serve requests, not for workers or jobs."
	case h.Phase == "maintenance":
		c.Status, c.Reason = api.ConditionFalse, h.Phase
		c.Message = "Maintenance mode answers every request with 503."
	case h.Phase == "stopped":
		c.Status, c.Reason, c.Terminal = api.ConditionFalse, h.Phase, true
		c.Message = "The service's replica target is zero."
	case h.Phase == "not_deployed" || (h.Phase == "deploying" && len(h.ServingDeploymentIDs) == 0):
		c.Status, c.Reason = api.ConditionFalse, h.Phase
		c.Message = "No release is serving yet."
		if hasLatest && latest.Status == checkFail {
			c.Terminal, c.Message = true, latest.Detail
		}
	case worst.Status == checkFail:
		c.Status, c.Reason, c.Message = api.ConditionFalse, worst.Code, worst.Detail
	case worst.Status == checkUnknown:
		c.Status, c.Reason, c.Message = api.ConditionUnknown, worst.Code, worst.Detail
	default:
		c.Status, c.Reason, c.Message = api.ConditionTrue, readyReason(h.Phase), "The app is serving."
		if worst.Status == checkWarning {
			c.Message = "The app is serving: " + worst.Detail
		}
		if h.Phase == "idle" {
			c.Message = "No instance is running; the next request starts one."
		}
	}
	return c
}

// readyReason is the phase of a ready app. A deployment in progress does not
// stop the serving release from being ready, so that phase reads as serving.
func readyReason(phase string) string {
	switch phase {
	case "idle", "starting":
		return phase
	}
	return "serving"
}

func appProgressing(h api.AppHealthResponse) api.Condition {
	c := api.Condition{Type: api.ConditionProgressing}
	latest, ok := check(h, checkLatestDeployment)
	switch {
	case ok && latest.Status == checkNotApplicable:
		c.Status, c.Reason = api.ConditionTrue, api.ConditionReasonDeploying
		c.Message = fmt.Sprintf("Deployment %s is in progress.", latest.DeploymentID)
	case ok && (latest.Status == checkWarning || latest.Status == checkFail):
		c.Status, c.Reason, c.Message = api.ConditionFalse, api.ConditionReasonLatestDeploymentFailed, latest.Detail
		c.Terminal = true
	case ok && latest.Status == checkUnknown:
		c.Status, c.Reason, c.Message = api.ConditionUnknown, latest.Code, latest.Detail
	default:
		c.Status, c.Reason, c.Message = api.ConditionFalse, api.ConditionReasonComplete, "No deployment is in progress."
	}
	return c
}

func appDegraded(h api.AppHealthResponse) api.Condition {
	c := api.Condition{Type: api.ConditionDegraded}
	worst, _ := worstCheck(h)
	switch h.Status {
	case healthDegraded, healthUnhealthy:
		c.Status, c.Reason, c.Message = api.ConditionTrue, worst.Code, worst.Detail
	case healthHealthy:
		c.Status, c.Reason, c.Message = api.ConditionFalse, api.ConditionReasonHealthy, h.Summary
	default:
		c.Status, c.Reason, c.Message = api.ConditionUnknown, worst.Code, worst.Detail
	}
	return c
}

func check(h api.AppHealthResponse, code string) (api.AppHealthCheck, bool) {
	for _, c := range h.Checks {
		if c.Code == code {
			return c, true
		}
	}
	return api.AppHealthCheck{}, false
}

// worstCheck is the first check of the highest severity, the one the
// assessment's summary names, leaving out the checks with the codes given.
func worstCheck(h api.AppHealthResponse, except ...string) (api.AppHealthCheck, bool) {
	rank := map[string]int{checkUnknown: 1, checkWarning: 2, checkFail: 3}
	var worst api.AppHealthCheck
	found := false
	for _, c := range h.Checks {
		if slices.Contains(except, c.Code) {
			continue
		}
		if rank[c.Status] > rank[worst.Status] {
			worst, found = c, true
		}
	}
	return worst, found
}

// Deployment returns a deployment's status from the deployments API response.
func Deployment(d api.DeploymentResponse, observedAt time.Time) api.ResourceStatus {
	return api.ResourceStatus{
		Kind: api.ResourceKindDeployment, Name: d.ID, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Conditions: []api.Condition{deploymentReady(d), deploymentProgressing(d), deploymentRolledOut(d)},
	}
}

func deploymentReady(d api.DeploymentResponse) api.Condition {
	c := api.Condition{Type: api.ConditionReady, Status: api.ConditionFalse, Reason: d.Status}
	switch {
	case d.Status == deploymentLive && ReadinessComplete(d):
		c.Status, c.Message = api.ConditionTrue, "The deployment is live."
	case d.Status == deploymentLive:
		c.Reason, c.Message = api.ConditionReasonVerifying, "The deployment is routable while its readiness checks finish."
	case terminalStatus(d.Status):
		c.Terminal, c.Message = true, terminalMessage(d)
	default:
		c.Message = fmt.Sprintf("The deployment is %s.", d.Status)
	}
	return c
}

func deploymentProgressing(d api.DeploymentResponse) api.Condition {
	c := api.Condition{Type: api.ConditionProgressing}
	switch {
	case terminalStatus(d.Status):
		c.Status, c.Reason, c.Terminal, c.Message = api.ConditionFalse, d.Status, true, terminalMessage(d)
	case d.Status != deploymentLive:
		c.Status, c.Reason, c.Message = api.ConditionTrue, d.Status, fmt.Sprintf("The deployment is %s.", d.Status)
	case !ReadinessComplete(d):
		c.Status, c.Reason = api.ConditionTrue, api.ConditionReasonVerifying
		c.Message = "Readiness checks are running."
	case rollingOut(d):
		c.Status, c.Reason, c.Message = api.ConditionTrue, api.ConditionReasonRollingOut, rolloutMessage(d)
	default:
		c.Status, c.Reason, c.Message = api.ConditionFalse, api.ConditionReasonComplete, "Nothing is in progress."
	}
	return c
}

func deploymentRolledOut(d api.DeploymentResponse) api.Condition {
	c := api.Condition{Type: api.ConditionRolledOut, Status: api.ConditionFalse}
	switch {
	case d.Status != deploymentLive:
		c.Reason, c.Terminal = d.Status, terminalStatus(d.Status)
		c.Message = fmt.Sprintf("The deployment is %s.", d.Status)
		if c.Terminal {
			c.Message = terminalMessage(d)
		}
	case d.CanaryTotalSteps > 0 && d.RolloutState == rolloutAborted:
		c.Reason, c.Terminal, c.Since = api.ConditionReasonRolloutAborted, true, timestamp(d.RolloutAbortedAt)
		c.Message = "The safe rollout stopped before 100% traffic."
		if d.RolloutAbortedReason != "" {
			c.Message = "The safe rollout stopped before 100% traffic: " + d.RolloutAbortedReason + "."
		}
	case rollingOut(d):
		c.Reason, c.Message = api.ConditionReasonRollingOut, rolloutMessage(d)
	default:
		c.Status, c.Reason, c.Since = api.ConditionTrue, api.ConditionReasonRolledOut, timestamp(d.RolloutCompletedAt)
		c.Message = "The deployment serves its full share of traffic."
	}
	return c
}

// ReadinessComplete reports whether a live deployment finished its readiness
// stage. A deployment is routable before that while hosting verification
// runs; a row without stage data predates the stages and is complete when
// live.
func ReadinessComplete(d api.DeploymentResponse) bool {
	if d.Status != deploymentLive {
		return false
	}
	if len(d.StageState) == 0 {
		return true
	}
	var stages struct {
		History []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"history"`
	}
	if json.Unmarshal(d.StageState, &stages) != nil {
		return false
	}
	for _, stage := range stages.History {
		if stage.Name == "readiness" && stage.Status == "completed" {
			return true
		}
	}
	return false
}

func rollingOut(d api.DeploymentResponse) bool {
	return d.CanaryTotalSteps > 0 && d.RolloutState != rolloutComplete && d.RolloutState != rolloutAborted
}

func rolloutMessage(d api.DeploymentResponse) string {
	return fmt.Sprintf("Safe rollout at step %d of %d.", d.CanaryStep, d.CanaryTotalSteps)
}

func terminalStatus(status string) bool {
	return status == deploymentFailed || status == deploymentCancelled || status == deploymentSuperseded
}

func terminalMessage(d api.DeploymentResponse) string {
	switch d.Status {
	case deploymentFailed:
		if d.Error != "" {
			return "The deployment failed: " + d.Error
		}
		return "The deployment failed."
	case deploymentCancelled:
		return "The deployment was cancelled."
	}
	return "A newer deployment replaced this one."
}

func timestamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
