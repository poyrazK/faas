package api

// Current graph evidence is a read-only observation, never deployment authority.
type EnvironmentWorkloadActivationEvidence struct {
	Workloads []EnvironmentWorkloadProgress `json:"workloads,omitempty"`
	// CommitSHA comes from the approved revision in the current evidence snapshot,
	// never from the independently polled Git ref. Older servers may omit it.
	CommitSHA                              string `json:"commit_sha,omitempty"`
	GraphID                                string `json:"graph_id"`
	SourceID                               string `json:"source_id"`
	EnvironmentID                          string `json:"environment_id"`
	RevisionID                             string `json:"revision_id"`
	DefinitionDigest                       string `json:"definition_digest"`
	Generation                             int64  `json:"generation"`
	IntentVersion                          int64  `json:"intent_version"`
	PlanHash                               string `json:"plan_hash"`
	GraphPhase                             string `json:"graph_phase"`
	GraphErrorCode                         string `json:"graph_error_code,omitempty"`
	ArtifactsPrepared                      bool   `json:"artifacts_prepared"`
	Candidates                             int    `json:"candidates"`
	RetainedWorkloadsRecorded              int    `json:"retained_workloads_recorded"`
	CapturesRecorded                       int    `json:"captures_recorded"`
	GuestConfigAcknowledgementsRecorded    int    `json:"guest_config_acknowledgements_recorded"`
	RestoresRecorded                       int    `json:"restores_recorded"`
	SmokesRecorded                         int    `json:"smokes_recorded"`
	JobSmokesRecorded                      int    `json:"job_smokes_recorded"`
	FrameworkReadyAcknowledgementsRecorded int    `json:"framework_ready_acknowledgements_recorded"`
	Qualified                              bool   `json:"qualified"`
	// Activated reflects exact active release-set membership for every graph
	// member; it does not prove that serving has converged.
	Activated bool `json:"activated"`
	// Serving requires its own committed convergence evidence.
	Serving         bool     `json:"serving"`
	BlockingReasons []string `json:"blocking_reasons"`
}

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
