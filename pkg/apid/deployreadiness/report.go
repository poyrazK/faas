// Package deployreadiness evaluates deployment intent using read-only state.
// It does not authorize an apply or execute application probes.
package deployreadiness

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type report struct {
	api.ProjectDeploymentReadiness
	identities map[string]any
}

func newReport(project string, req api.ProjectDeploymentCheckRequest) *report {
	return &report{ProjectDeploymentReadiness: api.ProjectDeploymentReadiness{
		SchemaVersion: 1, ProjectSlug: project, Environment: req.Environment,
		FromEnvironment: req.FromEnvironment, CheckedAt: time.Now().UTC(),
		Checks: []api.ProjectDeploymentCheck{},
	}, identities: map[string]any{}}
}
func (r *report) add(code, status, workload, message, remedy string) {
	if len(r.Checks) >= api.ProjectDeploymentCheckMaxChecks {
		return
	}
	if len(r.Checks) == api.ProjectDeploymentCheckMaxChecks-1 {
		r.Checks = append(r.Checks, api.ProjectDeploymentCheck{Code: "readiness_report_truncated", Status: "unknown", Message: "The readiness report reached its bounded check inventory.", Remedy: "Resolve the reported blockers and rerun the check to inspect the remaining inputs."})
		return
	}
	r.Checks = append(r.Checks, api.ProjectDeploymentCheck{Code: code, Status: status, Workload: workload, Message: message, Remedy: remedy})
}
func (r *report) quota(code string, observed, limit int, message, remedy string) {
	status := "passed"
	if observed > limit {
		status = "blocked"
	}
	r.add(code, status, "", message, remedy)
	if r.Checks[len(r.Checks)-1].Code != code {
		return
	}
	r.Checks[len(r.Checks)-1].Observed = &observed
	r.Checks[len(r.Checks)-1].Limit = &limit
}
func (r *report) finish(req api.ProjectDeploymentCheckRequest) api.ProjectDeploymentReadiness {
	complete := true
	for _, check := range r.Checks {
		complete = complete && check.Status != "unknown"
	}
	if complete {
		expected := req.ExpectedStateHash
		req.ExpectedStateHash = ""
		raw, _ := json.Marshal(struct {
			Request api.ProjectDeploymentCheckRequest
			State   map[string]any
		}{req, r.identities})
		r.StateHash = fmt.Sprintf("%x", sha256.Sum256(raw))
		if expected != "" && expected != r.StateHash {
			r.add("deployment_inputs_changed", "blocked", "", "Deployment inputs changed since the previous check.", "Review a fresh check and use its state hash for the next comparison.")
		}
	} else if req.ExpectedStateHash != "" {
		r.add("deployment_state_unavailable", "unknown", "", "Current inputs could not be compared with the previous check.", "Retry after the unavailable checks can be read.")
	}
	sort.SliceStable(r.Checks, func(i, j int) bool {
		a, b := r.Checks[i], r.Checks[j]
		return a.Code+"\x00"+a.Workload+"\x00"+a.Message < b.Code+"\x00"+b.Workload+"\x00"+b.Message
	})
	r.Status = api.DeploymentReadinessStatus(r.Checks)
	return r.ProjectDeploymentReadiness
}
