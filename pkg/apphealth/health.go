// Package apphealth assesses existing evidence without waking or probing workloads.
package apphealth

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	Healthy       = "healthy"
	Degraded      = "degraded"
	Unhealthy     = "unhealthy"
	Unknown       = "unknown"
	Pass          = "pass"
	Warning       = "warning"
	Fail          = "fail"
	NotApplicable = "not_applicable"
)

// Evidence is a request-time snapshot. Availability flags prevent a failed
// read from becoming a healthy empty slice. Readiness timestamps describe
// transitions, not heartbeat samples, so they do not expire like metrics.
type Evidence struct {
	App              state.App
	Live             []state.Deployment
	Latest           *state.Deployment
	DeploymentsKnown bool
	HistoryComplete  bool
	Instances        []state.Instance
	InstancesKnown   bool
	Readiness        map[string]map[string]state.InstanceReadiness
	Nodes            map[string]state.ComputeNode
	Metrics          appmetrics.RequestHealth
	MetricsAllowed   bool
	Now              time.Time
}

func Evaluate(e Evidence) api.AppHealthResponse {
	out := api.AppHealthResponse{
		AppID: e.App.ID, Scope: "default", Phase: "unknown",
		EvaluatedAt:          e.Now.UTC().Format(time.RFC3339Nano),
		ValidForSeconds:      int(api.AppHealthEvidenceMaxAge.Seconds()),
		ServingDeploymentIDs: []string{}, Checks: []api.AppHealthCheck{},
	}
	mode := e.App.Manifest.ExecutionMode
	if mode != "" && mode != "request" && mode != "service" {
		out.Phase = "unsupported"
		add(&out, "workload", Unknown, "HTTP serving health does not assess worker or job execution.", "configuration", "")
		return finish(out)
	}
	serving := servingDeployments(e.Live)
	for _, d := range serving {
		out.ServingDeploymentIDs = append(out.ServingDeploymentIDs, d.ID)
	}
	assessDeployments(&out, e, serving)
	if len(serving) > 0 && e.DeploymentsKnown {
		if out.Phase == "unknown" {
			out.Phase = "serving"
		}
		assessCapacity(&out, e, serving)
	}
	if e.App.MaintenanceMode {
		out.Phase = "maintenance"
		add(&out, "maintenance", Warning, "Maintenance mode intentionally returns 503 responses.", "configuration", "")
	}
	assessRequests(&out, e)
	return finish(out)
}

func servingDeployments(all []state.Deployment) []state.Deployment {
	var out []state.Deployment
	for _, d := range all {
		if (d.Scope == "" || d.Scope == "default") && d.Status == state.DeployLive && d.TrafficPercent > 0 {
			out = append(out, d)
		}
	}
	return out
}

func assessDeployments(out *api.AppHealthResponse, e Evidence, live []state.Deployment) {
	if !e.DeploymentsKnown {
		add(out, "deployment", Unknown, "Serving deployment evidence is unavailable.", "deployments", "")
	}
	if e.Latest != nil {
		out.LatestDeploymentID = e.Latest.ID
		if e.Latest.Status == state.DeployFailed {
			severity := Warning
			if e.DeploymentsKnown && len(live) == 0 {
				severity = Fail
			}
			add(out, "latest_deployment", severity, "The latest default-scope deployment failed. Any older serving release is assessed separately.", "deployments", e.Latest.ID)
		} else if e.Latest.Status.IsCancelEligible() {
			out.Phase = "deploying"
			add(out, "latest_deployment", NotApplicable, "A default-scope deployment is in progress; it has not replaced serving health evidence.", "deployments", e.Latest.ID)
		}
	} else if !e.HistoryComplete {
		add(out, "latest_deployment", Unknown, "The latest default-scope deployment could not be confirmed from the bounded history read.", "deployments", "")
	}
	if !e.DeploymentsKnown {
		return
	}
	if len(live) == 0 {
		if out.Phase != "deploying" {
			out.Phase = "not_deployed"
		}
		add(out, "deployment", Unknown, "No traffic-bearing default-scope release is available to assess.", "deployments", "")
		return
	}
	add(out, "deployment", Pass, "Traffic-bearing default-scope releases are present.", "", "")
}

func assessCapacity(out *api.AppHealthResponse, e Evidence, live []state.Deployment) {
	required := e.App.MinInstances
	service := e.App.Manifest.ExecutionMode == "service"
	if service {
		required = 1
		if e.App.Manifest.ServiceReplicas != nil {
			required = e.App.Manifest.ServiceReplicas.Desired
		}
	}
	out.Capacity.Required = required
	if !e.InstancesKnown {
		add(out, "readiness", Unknown, "Instance evidence is unavailable or exceeds the bounded scan.", "logs", "")
		return
	}
	out.Capacity.Known = true
	readyByDeployment := make(map[string]int)
	deployments := make(map[string]state.Deployment, len(live))
	for _, d := range live {
		deployments[d.ID] = d
	}
	var findings []api.AppHealthFinding
	instances := slices.Clone(e.Instances)
	slices.SortFunc(instances, func(a, b state.Instance) int {
		if a.DeploymentID != b.DeploymentID {
			return strings.Compare(a.DeploymentID, b.DeploymentID)
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, i := range instances {
		d, ok := deployments[i.DeploymentID]
		if !ok || i.Kind != "" || (i.Mode != "" && i.Mode != "normal" && i.Mode != "service") {
			continue
		}
		switch state.State(i.State) {
		case state.StateRunning:
			status, issues := instanceReadiness(e, i, d)
			findings = append(findings, issues...)
			switch status {
			case Pass:
				out.Capacity.Ready++
				readyByDeployment[d.ID]++
			case Fail:
				out.Capacity.Unready++
			default:
				out.Capacity.Unknown++
			}
		case state.StateWaking, state.StateColdBooting:
			out.Capacity.Starting++
		}
	}
	if service && required > 0 && out.Capacity.Ready >= required {
		for _, d := range live {
			if readyByDeployment[d.ID] == 0 {
				add(out, "traffic_readiness", Warning, "A traffic-bearing service release has no confirmed ready replica, even though the total replica target is met.", "deployments", d.ID)
				break
			}
		}
	}
	c := out.Capacity
	switch {
	case service && required == 0:
		out.Phase = "stopped"
		add(out, "readiness", NotApplicable, "Service replica target is zero; the service is intentionally stopped.", "configuration", "")
	case c.Unready > 0:
		severity := Warning
		if c.Ready == 0 {
			severity = Fail
		}
		add(out, "readiness", severity, fmt.Sprintf("%d ready, %d unready replicas. Required probes or node availability report a failure.", c.Ready, c.Unready), "logs", "")
	case c.Ready < required:
		severity := Warning
		if c.Ready == 0 && c.Starting == 0 && c.Unknown == 0 {
			severity = Fail
		}
		if c.Unknown > 0 {
			severity = Unknown
		}
		add(out, "readiness", severity, fmt.Sprintf("%d of %d required replicas are ready; %d starting, %d unconfirmed.", c.Ready, required, c.Starting, c.Unknown), "logs", "")
	case c.Unknown > 0:
		add(out, "readiness", Unknown, "Some running replicas have unconfirmed readiness or node evidence.", "logs", "")
	case c.Ready == 0 && c.Starting > 0:
		out.Phase = "starting"
		add(out, "readiness", Warning, "Replicas are starting; serving readiness is not yet confirmed.", "logs", "")
	case c.Ready == 0:
		out.Phase = "idle"
		recoverable := true
		for _, d := range live {
			if d.RootfsPath == "" && d.RootfsKey == "" {
				recoverable = false
			}
		}
		if recoverable {
			add(out, "readiness", Pass, "No warm replicas are required. Releases have cold-boot artifacts; scale-to-zero idle is expected. A wake has not been tested by this assessment.", "", "")
		} else {
			add(out, "readiness", Unknown, "No ready replica or confirmed cold-boot artifact is available.", "deployments", "")
		}
	default:
		add(out, "readiness", Pass, fmt.Sprintf("%d replicas passed the configured readiness gates and have current node evidence.", c.Ready), "", "")
	}
	check := &out.Checks[len(out.Checks)-1]
	if !service || required != 0 {
		slices.SortStableFunc(findings, func(a, b api.AppHealthFinding) int {
			return strings.Compare(a.Status, b.Status) // fail before unknown; preserve target order within severity.
		})
		check.FindingsTruncated = len(findings) > api.AppHealthFindingLimit
		check.Findings = findings[:min(len(findings), api.AppHealthFindingLimit)]
		// Put a known failure ahead of missing evidence in the headline, even
		// when the bounded detail list starts with an unavailable source.
		for _, f := range findings {
			if f.Status == Fail {
				check.Detail = fmt.Sprintf("%d ready, %d unready replicas. %s: %s", c.Ready, c.Unready, f.Source, f.Detail)
				break
			}
		}
	}
	if c.Ready < required && c.Unready == 0 {
		check.Reason = "capacity_below_target"
	}
}

// Match the gateway's independent primary-app and primary-ingress gates.
// Decode only routing fields, never companion environment or probe output.
func requiredSources(d state.Deployment) ([]string, error) {
	var sources []string
	if len(d.OverrideReadinessProbe) > 0 {
		var p api.DeploymentReadinessProbe
		if err := json.Unmarshal(d.OverrideReadinessProbe, &p); err != nil {
			return nil, fmt.Errorf("decode readiness: %w", err)
		}
		if p.Path != "" || p.GRPC != nil {
			sources = append(sources, "primary_app")
		}
	}
	var companions []struct {
		Name           string            `json:"name"`
		Type           api.SidecarType   `json:"type"`
		PrimaryIngress bool              `json:"primary_ingress"`
		ReadinessProbe *api.SidecarProbe `json:"readiness_probe"`
	}
	if len(d.Sidecars) > 0 {
		if err := json.Unmarshal(d.Sidecars, &companions); err != nil {
			return nil, fmt.Errorf("decode companions: %w", err)
		}
	}
	for _, c := range companions {
		if c.Type == api.SidecarTypeSidecar && c.PrimaryIngress && c.ReadinessProbe != nil && c.Name != "" {
			sources = append(sources, "sidecar:"+c.Name)
		}
	}
	return sources, nil
}

func add(out *api.AppHealthResponse, code, status, detail, action, deploymentID string) {
	out.Checks = append(out.Checks, api.AppHealthCheck{Code: code, Status: status, Detail: detail, Action: action, DeploymentID: deploymentID})
}

func finish(out api.AppHealthResponse) api.AppHealthResponse {
	out.Status = Healthy
	rank := map[string]int{Pass: 0, NotApplicable: 0, Unknown: 1, Warning: 2, Fail: 3}
	max := 0
	for _, c := range out.Checks {
		if rank[c.Status] > max {
			max = rank[c.Status]
			out.Summary = c.Detail
		}
	}
	switch max {
	case 1:
		out.Status = Unknown
	case 2:
		out.Status = Degraded
	case 3:
		out.Status = Unhealthy
	default:
		out.Summary = "Available evidence shows no health issues. This assessment does not probe the public endpoint."
	}
	return out
}
