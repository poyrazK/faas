package state

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
)

// Schedd publishes the runtime returned by vmmd and the inputs it delivered in
// one transaction. Qualification VM creation returns only after guest-init has
// acknowledged the exact main API-env digest, selected secret-key set and each
// prepared sidecar environment for that attempt. This delivery acknowledgement
// does not qualify or activate the graph.
type EnvironmentGitOpsQualificationRuntimeStore interface {
	PublishEnvironmentWorkloadQualificationRuntime(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentWorkloadQualificationRuntime) (Instance, error)
}

type EnvironmentWorkloadQualificationRuntime struct {
	NodeID   string
	WakeID   string
	Netns    string
	HostIP   string
	GuestUID int
	Inputs   RuntimeConfigInputs
}

func qualificationRuntimeValid(runtime EnvironmentWorkloadQualificationRuntime) bool {
	_, nodeErr := uuid.Parse(runtime.NodeID)
	_, wakeErr := uuid.Parse(runtime.WakeID)
	_, ipErr := netip.ParseAddr(runtime.HostIP)
	return nodeErr == nil && wakeErr == nil && ipErr == nil && runtime.Netns != "" && runtime.GuestUID > 0 && validateRuntimeConfigInputs(runtime.Inputs) == nil
}

func qualificationRuntimeMatches(ins Instance, request EnvironmentWorkloadQualificationRequest, runtime EnvironmentWorkloadQualificationRuntime) bool {
	return ins.ID == request.ReservedInstanceID && ins.AppID == request.AppID && ins.DeploymentID == request.DeploymentID &&
		ins.NodeID == runtime.NodeID && ins.WakeID == runtime.WakeID && ins.Mode == qualificationInstanceMode(request) &&
		ins.Kind == "wake" && ins.JobID == "" && runtime.Inputs.Scope == request.FrozenInputs.Scope
}

func qualificationRuntimeAlreadyPublished(ins Instance, runtime EnvironmentWorkloadQualificationRuntime) bool {
	return State(ins.State) == StateRunning && ins.Netns == runtime.Netns && ins.HostIP == runtime.HostIP && ins.GuestUID == runtime.GuestUID
}
