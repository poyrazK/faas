package api

import "time"

// EnvironmentGitOpsExplanation describes recorded facts, never an execution
// capability. Unknown stages must not be interpreted as readiness.
type EnvironmentGitOpsExplanation struct {
	ReadOnly           bool                          `json:"read_only"`
	AssessedAt         time.Time                     `json:"assessed_at"`
	Status             string                        `json:"status"`
	Summary            string                        `json:"summary"`
	SourceID           string                        `json:"source_id"`
	Generation         int64                         `json:"generation"`
	IntentVersion      int64                         `json:"intent_version"`
	SourceFreshness    string                        `json:"source_freshness"`
	ObservedCommitSHA  string                        `json:"observed_commit_sha,omitempty"`
	ApprovedRevisionID string                        `json:"approved_revision_id,omitempty"`
	AppliedRevisionID  string                        `json:"applied_revision_id,omitempty"`
	ServingRevisionID  string                        `json:"serving_revision_id,omitempty"`
	Issues             []EnvironmentGitOpsDiagnostic `json:"issues"`
	Workloads          []EnvironmentWorkloadProgress `json:"workloads"`
}

type EnvironmentGitOpsDiagnostic struct {
	Resource string `json:"resource,omitempty"`
	Stage    string `json:"stage"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
}

// Stages are complete, pending, failed or unknown. BuildStatus is a recorded
// builder state, not proof of qualification, activation or serving.
type EnvironmentWorkloadProgress struct {
	Resource            string   `json:"resource"`
	AppID               string   `json:"app_id"`
	DeploymentID        string   `json:"deployment_id,omitempty"`
	JobID               string   `json:"job_id,omitempty"`
	ExecutionMode       string   `json:"execution_mode"`
	BuildStatus         string   `json:"build_status,omitempty"`
	BuildCacheStatus    string   `json:"build_cache_status,omitempty"`
	BuildCacheKeySHA256 string   `json:"build_cache_key_sha256,omitempty"`
	Preparation         string   `json:"preparation"`
	Qualification       string   `json:"qualification"`
	Activation          string   `json:"activation"`
	Serving             string   `json:"serving"`
	BlockingReasons     []string `json:"blocking_reasons"`
}
