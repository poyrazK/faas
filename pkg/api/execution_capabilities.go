package api

// ExecutionProfileCapability describes one profile recognized by the Runs
// admission contract and the interpreter versions it accepts.
type ExecutionProfileCapability struct {
	Profile  ExecutionProfile   `json:"profile"`
	Runtimes []ExecutionRuntime `json:"runtimes"`
	Packages map[string]string  `json:"packages,omitempty"`
}

// ExecutionCapabilityLimits contains the account plan's Runs envelope and
// the protocol caps an agent must observe when building a request.
type ExecutionCapabilityLimits struct {
	MaxConcurrentRuns      int `json:"max_concurrent_runs"`
	MaxSourceBytes         int `json:"max_source_bytes"`
	MaxInputBytes          int `json:"max_input_bytes"`
	DefaultOutputBytes     int `json:"default_output_bytes"`
	MaxOutputBytes         int `json:"max_output_bytes"`
	DefaultTimeoutMS       int `json:"default_timeout_ms"`
	MaxTimeoutMS           int `json:"max_timeout_ms"`
	DefaultMemoryMB        int `json:"default_memory_mb"`
	MaxMemoryMB            int `json:"max_memory_mb"`
	DefaultCPUMillicores   int `json:"default_cpu_millicores"`
	MaxCPUMillicores       int `json:"max_cpu_millicores"`
	DefaultEphemeralDiskMB int `json:"default_ephemeral_disk_mb"`
	MaxEphemeralDiskMB     int `json:"max_ephemeral_disk_mb"`
	PIDsMax                int `json:"pids_max"`
	MaxBundleFiles         int `json:"max_bundle_files"`
	MaxArtifactInputs      int `json:"max_artifact_inputs"`
	MaxOutputFiles         int `json:"max_output_files"`
	MaxArtifactPathBytes   int `json:"max_artifact_path_bytes"`
}

// ExecutionCapabilitiesResponse gives an authenticated caller the Runs
// admission switch, supported request contract, and plan-resolved limits.
type ExecutionCapabilitiesResponse struct {
	Plan                string                       `json:"plan"`
	AdmissionAvailable  bool                         `json:"admission_available"`
	PlanEntitled        bool                         `json:"plan_entitled"`
	ControlPlaneEnabled bool                         `json:"control_plane_enabled"`
	UnavailableReasons  []string                     `json:"unavailable_reasons,omitempty"`
	Runtimes            []ExecutionRuntime           `json:"runtimes"`
	Profiles            []ExecutionProfileCapability `json:"profiles"`
	NetworkModes        []ExecutionNetworkMode       `json:"network_modes"`
	Limits              *ExecutionCapabilityLimits   `json:"limits,omitempty"`
}
