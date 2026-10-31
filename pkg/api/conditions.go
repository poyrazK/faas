package api

// ResourceStatus is a resource's status subresource (ADR-20261030151810):
// its conditions, derived when read from evidence the API already holds.
// ObservedAt is when that evidence was read, so a reader knows how fresh
// the conditions are.
type ResourceStatus struct {
	Kind       string      `json:"kind"`
	Name       string      `json:"name"`
	ObservedAt string      `json:"observed_at"`
	Conditions []Condition `json:"conditions"`
}

// Condition is one named aspect of a resource's state, in the shape of a
// Kubernetes status condition. Reason is a stable code from the closed list
// below, safe to branch on; Message is for people. Since is set only from a
// recorded timestamp. Terminal marks a condition that will not change
// without a new request, so a wait can stop on it.
type Condition struct {
	Type     string `json:"type"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Since    string `json:"since,omitempty"`
	Terminal bool   `json:"terminal,omitempty"`
}

// Condition statuses.
const (
	ConditionTrue    = "True"
	ConditionFalse   = "False"
	ConditionUnknown = "Unknown"
)

// Resource kinds with a status subresource.
const (
	ResourceKindApp        = "app"
	ResourceKindDeployment = "deployment"
)

// Condition types.
const (
	ConditionReady       = "Ready"
	ConditionProgressing = "Progressing"
	ConditionDegraded    = "Degraded"
	ConditionRolledOut   = "RolledOut"
)

// ConditionTypes are the condition types each kind reports, in the order
// they are returned.
var ConditionTypes = map[string][]string{
	ResourceKindApp:        {ConditionReady, ConditionProgressing, ConditionDegraded},
	ResourceKindDeployment: {ConditionReady, ConditionProgressing, ConditionRolledOut},
}

// Condition reasons that are not an app health phase, an app health check
// code or a deployment status, which are reasons as they are.
const (
	ConditionReasonNotAssessed            = "not_assessed"
	ConditionReasonDeploying              = "deploying"
	ConditionReasonComplete               = "complete"
	ConditionReasonLatestDeploymentFailed = "latest_deployment_failed"
	ConditionReasonHealthy                = "healthy"
	ConditionReasonVerifying              = "verifying"
	ConditionReasonRollingOut             = "rolling_out"
	ConditionReasonRolledOut              = "rolled_out"
	ConditionReasonRolloutAborted         = "rollout_aborted"
)
