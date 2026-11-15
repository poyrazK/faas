package mcphosting

// TaskReleaseReview is an offline source review, not an executable publication
// plan. It contains neither credential references nor credential values.
type TaskReleaseReview struct {
	Version            int              `json:"version"`
	Status             string           `json:"status"`
	Fingerprint        string           `json:"release_fingerprint"`
	Protocol           string           `json:"protocol"`
	WebApp             string           `json:"web_app"`
	WorkerApp          string           `json:"worker_app"`
	WorkerRuntime      string           `json:"worker_runtime"`
	ObserverApp        string           `json:"observer_app"`
	ObserverMetricApp  string           `json:"observer_metric_app"`
	PreviousWorkerApps []string         `json:"previous_worker_apps"`
	Endpoint           string           `json:"endpoint"`
	AccessMode         string           `json:"access_mode"`
	ToolScopes         ScopePolicy      `json:"tool_scopes"`
	ReadinessRoles     []string         `json:"readiness_roles"`
	RequiredSetup      []string         `json:"required_setup"`
	TimeoutSeconds     int              `json:"release_timeout_seconds"`
	WebLimits          TaskReviewLimits `json:"web_configured_limits"`
	WorkerLimits       TaskReviewLimits `json:"worker_configured_limits"`
	MigrationsRequired bool             `json:"migrations_required"`
	BindingsVerified   bool             `json:"bindings_verified"`
	ReadyToApply       bool             `json:"ready_to_apply"`
}

// TaskReviewLimits contains only explicitly configured overrides. Omitted
// values retain starter defaults; deployed quotas and bindings need live checks.
type TaskReviewLimits struct {
	TTLSeconds             int `json:"ttl_seconds,omitempty"`
	WorkerConcurrency      int `json:"worker_concurrency,omitempty"`
	MaxAttempts            int `json:"max_attempts,omitempty"`
	MaxRunning             int `json:"max_running,omitempty"`
	MaxRunningPerOwner     int `json:"max_running_per_owner,omitempty"`
	MaxOutstanding         int `json:"max_outstanding,omitempty"`
	MaxOutstandingPerOwner int `json:"max_outstanding_per_owner,omitempty"`
	ShutdownTimeoutMS      int `json:"shutdown_timeout_ms,omitempty"`
}

// ReviewTaskLimits omits all credential references from configured Task limits.
func ReviewTaskLimits(c TasksConfig) TaskReviewLimits {
	return TaskReviewLimits{TTLSeconds: c.TTLSeconds, WorkerConcurrency: c.WorkerConcurrency,
		MaxAttempts: c.MaxAttempts, MaxRunning: c.MaxRunning, MaxRunningPerOwner: c.MaxRunningPerOwner,
		MaxOutstanding: c.MaxOutstanding, MaxOutstandingPerOwner: c.MaxOutstandingPerOwner,
		ShutdownTimeoutMS: c.ShutdownTimeoutMS}
}
