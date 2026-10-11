package api

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// EnvironmentDefinition is the versioned, non-secret customer GitOps contract.
// Omitted fields are unmanaged. Collection ownership is explicit: routes and
// policies are replaced as a whole, while maps own only their declared keys.
type EnvironmentDefinition struct {
	APIVersion  string `json:"api_version"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	// Retain is the only supported queue pruning disposition. It retires
	// admission/dispatch while preserving accepted work and receipt identity.
	QueuePruningPolicy string                     `json:"queue_pruning_policy,omitempty"`
	Configuration      map[string]json.RawMessage `json:"configuration,omitempty"`
	// An explicit empty map represents an empty environment. Missing or null
	// membership is invalid; deletion still requires a reviewed prune plan.
	Workloads map[string]EnvironmentWorkload `json:"workloads"`
}

type EnvironmentWorkload struct {
	// App is an existing Gregale slug for adoption. New workloads may omit it;
	// the persisted identity mapping determines their eventual app ID.
	App           string                             `json:"app,omitempty"`
	Source        *EnvironmentWorkloadSource         `json:"source,omitempty"`
	Runtime       json.RawMessage                    `json:"runtime,omitempty"`
	Variables     map[string]string                  `json:"variables,omitempty"`
	SecretRefs    map[string]string                  `json:"secret_refs,omitempty"`
	Routes        *EnvironmentRouteContract          `json:"routes,omitempty"`
	Policies      *[]EnvironmentPolicy               `json:"policies,omitempty"`
	QueueBindings map[string]EnvironmentQueueBinding `json:"queue_bindings,omitempty"`
	// QueueSmoke supplies one bounded, reviewed synthetic message per active
	// worker binding or HTTP-function push binding. Qualification dispatches it
	// directly to the private candidate VM; it never enqueues a customer message.
	QueueSmoke map[string]EnvironmentQueueSmoke `json:"queue_smoke,omitempty"`
	// JobSmoke is the reviewed one-shot argv and timeout for an isolated job
	// qualification run. It is frozen with the candidate and never creates a
	// customer JobRun or scheduled execution.
	JobSmoke *EnvironmentJobSmoke `json:"job_smoke,omitempty"`
	// Schedule configures a production recurring job. Qualification remains
	// separate: this contract is not a job run or evidence that dispatch is
	// enabled. The schedule adapter links it to a durable Gregale Job.
	Schedule *EnvironmentJobSchedule `json:"schedule,omitempty"`
	// Recovery pins an original retained binding UUID. Reviewed adoption
	// preserves its hold; subsequent reconciliation may resume that identity.
	QueueRecoveries map[string]string                    `json:"queue_recoveries,omitempty"`
	ServiceBindings map[string]EnvironmentServiceBinding `json:"service_bindings,omitempty"`
}

type EnvironmentWorkloadSource struct {
	Kind string `json:"kind"`
	// Runtime is required for kind function and selects its supported runner.
	Runtime    string `json:"runtime,omitempty"`
	Directory  string `json:"directory,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
	Image      string `json:"image,omitempty"`
}

type EnvironmentRouteContract struct {
	OnlyAllowDeclaredRoutes bool            `json:"only_allow_declared_routes"`
	DeclaredRoutes          []DeclaredRoute `json:"declared_routes"`
}

// EnvironmentPolicy includes only policy kinds with durable environment
// scope. Add a kind only together with an observer, executor, and write guard.
type EnvironmentPolicy struct {
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	MatchPath    string            `json:"match_path"`
	MatchMethods []string          `json:"match_methods,omitempty"`
	MatchHeaders map[string]string `json:"match_headers,omitempty"`
	Priority     int               `json:"priority"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Action       json.RawMessage   `json:"action"`
}

type EnvironmentQueueBinding struct {
	QueueName string `json:"queue_name"`
	Mode      string `json:"mode,omitempty"`
	// WorkloadClass is worker, job, or HTTP for an HTTP function using push.
	WorkloadClass  string          `json:"workload_class"`
	Enabled        *bool           `json:"enabled,omitempty"`
	MaxConcurrency int             `json:"max_concurrency,omitempty"`
	RetryPolicy    *RetryPolicyDTO `json:"retry_policy,omitempty"`
}

// EnvironmentQueueSmoke is the customer-authored JSON body used to verify a
// queue handler on an isolated candidate. It is not sent through the customer
// queue and must not contain secrets.
type EnvironmentQueueSmoke struct {
	Payload json.RawMessage `json:"payload"`
}

// EnvironmentJobSmoke is an immutable, argv-only process contract. The
// qualification runner never interprets it through a shell and accepts only
// a bounded successful exit within TimeoutSeconds.
type EnvironmentJobSmoke struct {
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// EnvironmentJobSchedule owns the cron trigger and the scheduler policies
// that Gregale persists on a recurring Job. The workload's immutable source
// and runtime settings supply its executable; schedule policy does not change
// the isolated job_smoke contract.
type EnvironmentJobSchedule struct {
	Cron           string                     `json:"cron"`
	Timezone       string                     `json:"timezone"`
	SchedulePolicy *workpolicy.SchedulePolicy `json:"schedule_policy,omitempty"`
	FailureRules   *workpolicy.FailureRules   `json:"failure_rules,omitempty"`
}

type EnvironmentServiceBinding struct {
	Workload string `json:"workload"`
	EnvKey   string `json:"env_key"`
}
