package apphealth

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Return every independent failure, even when other sources are unavailable.
// No probe response, deployment error or infrastructure address is exposed.
func instanceReadiness(e Evidence, i state.Instance, d state.Deployment) (string, []api.AppHealthFinding) {
	var findings []api.AppHealthFinding
	if issue := nodeFinding(e, i); issue != nil {
		findings = append(findings, *issue)
	}
	sources, err := requiredSources(d)
	if err != nil {
		findings = append(findings, finding(i, Unknown, "readiness_configuration_unavailable", "Required readiness sources could not be decoded.", "configuration", time.Time{}))
	}
	for _, source := range sources {
		signal, ok := e.Readiness[i.ID][source]
		switch {
		case !ok:
			findings = append(findings, finding(i, Unknown, "readiness_missing", "No readiness transition has been recorded for this required probe.", source, time.Time{}))
		case signal.At.IsZero() || signal.At.After(e.Now.Add(api.AppHealthEvidenceMaxAge)):
			findings = append(findings, finding(i, Unknown, "readiness_timestamp_invalid", "This required probe has no valid readiness transition time.", source, time.Time{}))
		case !signal.Ready:
			findings = append(findings, finding(i, Fail, "required_probe_unready", "This required probe reports unready.", source, signal.At))
		}
	}
	status := Pass
	for _, f := range findings {
		if f.Status == Fail {
			return Fail, findings
		}
		status = Unknown
	}
	return status, findings
}

func nodeFinding(e Evidence, i state.Instance) *api.AppHealthFinding {
	node, ok := e.Nodes[i.NodeID]
	status, reason, detail := Unknown, "node_evidence_missing", "Compute node liveness evidence is unavailable."
	switch {
	case !ok:
	case node.Lifecycle == state.NodeLifecycleUnavailable || node.Lifecycle == "" && !node.Active:
		status, reason, detail = Fail, "node_unavailable", "The compute node is marked unavailable."
	case node.LastHeartbeatAt.IsZero():
	case node.LastHeartbeatAt.After(e.Now.Add(api.AppHealthEvidenceMaxAge)):
		reason, detail = "node_timestamp_invalid", "The compute node heartbeat time is invalid."
	case e.Now.Sub(node.LastHeartbeatAt) > state.DefaultHeartbeatStaleness:
		status, reason, detail = Fail, "node_heartbeat_stale", "The compute node heartbeat exceeds the routing liveness allowance."
	case node.Lifecycle == state.NodeLifecycleRecovering:
		reason, detail = "node_recovering", "The compute node is recovering; serving availability is unconfirmed."
	default:
		return nil
	}
	f := finding(i, status, reason, detail, "node", node.LastHeartbeatAt)
	return &f
}

func finding(i state.Instance, status, reason, detail, source string, at time.Time) api.AppHealthFinding {
	f := api.AppHealthFinding{Reason: reason, Status: status, Detail: detail, DeploymentID: i.DeploymentID, InstanceID: i.ID, Source: source}
	if !at.IsZero() {
		f.ObservedAt = at.UTC().Format(time.RFC3339Nano)
	}
	return f
}
