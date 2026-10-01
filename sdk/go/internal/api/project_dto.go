package api

type ServiceBindingTransport string

const (
	ServiceBindingTransportHTTP  ServiceBindingTransport = "http"
	ServiceBindingTransportHTTPS ServiceBindingTransport = "https"
)

type ServiceCallScope struct {
	Methods      []string `json:"methods"`
	PathPrefixes []string `json:"path_prefixes"`
}
type ServiceCallerScopes map[string]ServiceCallScope

type ProjectSourceRefScanRequest struct {
	ComputeRegion    string   `json:"compute_region,omitempty"`
	Repo             string   `json:"repo"`
	Ref              string   `json:"ref"`
	ProjectSlug      string   `json:"project_slug"`
	RepoFullName     string   `json:"repo_full_name,omitempty"`
	ProductionBranch string   `json:"production_branch,omitempty"`
	InstallID        int64    `json:"install_id,omitempty"`
	Only             []string `json:"only,omitempty"`
	Exclude          []string `json:"exclude,omitempty"`
	Environment      string   `json:"environment,omitempty"`
	NoTriggers       bool     `json:"no_triggers,omitempty"`
}

type PlanWorkload struct {
	ComputeRegion string   `json:"compute_region,omitempty"`
	Name          string   `json:"name"`
	RootDir       string   `json:"root_dir"`
	Dockerfile    string   `json:"dockerfile,omitempty"`
	Command       []string `json:"command"`
	DependsOn     []string `json:"depends_on,omitempty"`

	ServiceBindingPolicy ServiceBindingPolicy `json:"service_binding_policy,omitempty"`
	// ServiceBindingTransport opts a Compose workload into the HTTPS-first
	// canonical URL contract. Omitted keeps the established transport.
	ServiceBindingTransport   ServiceBindingTransport             `json:"service_binding_transport,omitempty"`
	ServiceReliability        map[string]ServiceReliabilityPolicy `json:"service_reliability,omitempty"`
	PreviewServiceCallsPolicy PreviewServiceCallsPolicy           `json:"preview_service_calls_policy,omitempty"`
	AllowedServiceCallers     *[]string                           `json:"allowed_service_callers,omitempty"`
	AllowedServiceCallScopes  *ServiceCallerScopes                `json:"allowed_service_call_scopes,omitempty"`
	PlatformTenantRequired    *bool                               `json:"platform_tenant_required,omitempty"`

	Class         string   `json:"class,omitempty"`
	Schedule      string   `json:"schedule,omitempty"`
	Ports         []int    `json:"ports"`
	EnvKeys       []string `json:"env_keys,omitempty"`
	Source        string   `json:"source,omitempty"`
	Tier          string   `json:"tier,omitempty"`
	Action        string   `json:"action,omitempty"`          // ADR-124: "create" | "update"
	ExistingAppID string   `json:"existing_app_id,omitempty"` // ADR-124: empty iff Action == "create"
	// DetectedBy is the structured detection trace (issue #742).
	// Pointer + omitempty so the field is purely additive: an
	// existing `gregale scan --json` consumer that does not know
	// the key sees byte-identical output for any response the
	// server did not populate.
	DetectedBy *PlanDetectedBy `json:"detected_by,omitempty"`
}

type PlanDetectedBy struct {
	// Detector is the closed vocabulary from reposcan's detector
	// enum: compose | procfile | k8s | render | fly | serverless |
	// app_yaml | other. Pinned by the OpenAPI enum; renaming a
	// value is a wire-contract change.
	Detector string `json:"detector"`
	// Marker is the concrete source marker for the winning detector,
	// such as "compose.yaml" or "Procfile". It excludes the workload
	// name, which remains available in PlanWorkload.Name.
	Marker string `json:"marker"`
	// Priority is the detector's tiebreak weight. Higher wins
	// identity within a tier. Surfaced so the precedence order is
	// visible on the wire rather than implied by our source.
	Priority int `json:"priority"`
	// MergedFrom names the other detectors whose seeds collapsed
	// into this workload under the (root_dir, name) merge key,
	// deduplicated and deterministically ordered. Absent when the
	// workload came from a single seed.
	//
	// This is what answers "why didn't my Procfile `web` get its
	// own workload?" — it merged into the compose `web`.
	MergedFrom []string `json:"merged_from,omitempty"`
}

type PlanDetectionWarning struct {
	Workload string `json:"workload,omitempty"`
	Detector string `json:"detector"`
	Marker   string `json:"marker"`
	Priority int    `json:"priority"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason"`
}

type PlanManaged struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	EnvHint string `json:"env_hint"`
	Source  string `json:"source"`
	Image   string `json:"image"`
}

type PlanCron struct {
	WorkloadName string `json:"workload_name"`
	Schedule     string `json:"schedule"`
	Path         string `json:"path"`
	Enabled      bool   `json:"enabled"`
}

type PlanAsyncRoute struct {
	App           string          `json:"app"`
	Name          string          `json:"name"`
	Action        string          `json:"action"` // create | update | remove | unchanged | skipped
	MatchHost     string          `json:"match_host"`
	MatchPath     string          `json:"match_path"`
	MatchMethods  []string        `json:"match_methods"`
	Priority      int             `json:"priority"`
	Enabled       bool            `json:"enabled"`
	OnSuccess     string          `json:"on_success,omitempty"`
	OnFailure     string          `json:"on_failure,omitempty"`
	RetryPolicy   *RetryPolicyDTO `json:"retry_policy,omitempty"`
	MaxAgeSeconds int             `json:"max_age_seconds,omitempty"`
	Reason        string          `json:"reason,omitempty"`
}

type PlanAffectedApp struct {
	Slug            string `json:"slug"`
	ID              string `json:"id,omitempty"` // empty iff Action == "create"
	Action          string `json:"action"`       // "create" | "update" | "remove" | "noop"
	ExistingRootDir string `json:"existing_root_dir,omitempty"`
}

type PlanResponse struct {
	ComputeRegion         string                 `json:"compute_region,omitempty"`
	ProjectSlug           string                 `json:"project_slug"`
	RepoFullName          string                 `json:"repo_full_name,omitempty"`
	Environment           string                 `json:"environment,omitempty"`
	EnvironmentProtected  bool                   `json:"environment_protected,omitempty"`
	EnvironmentConfigHash string                 `json:"environment_config_hash,omitempty"`
	ScanSource            string                 `json:"scan_source"`
	Tier                  string                 `json:"tier"`
	Workloads             []PlanWorkload         `json:"workloads"`
	Managed               []PlanManaged          `json:"managed"`
	Crons                 []PlanCron             `json:"crons"`
	AsyncRoutes           []PlanAsyncRoute       `json:"async_routes,omitempty"`
	Warnings              []string               `json:"warnings,omitempty"`
	DetectionWarnings     []PlanDetectionWarning `json:"detection_warnings,omitempty"`
	ObservedApps          int                    `json:"observed_apps"`
	ObservedCrons         int                    `json:"observed_crons"`
	LimitApps             int                    `json:"limit_apps"`
	LimitCrons            int                    `json:"limit_crons"`
	CanApply              bool                   `json:"can_apply"`
	CronsNotAllowed       bool                   `json:"crons_not_allowed,omitempty"`
	PlanToken             string                 `json:"plan_token"`
	// ADR-124 can_apply rescue signal. PreExclude is the gate
	// evaluated on the full scan (pre-`--only`/pre-`--exclude`).
	// Rescued is true when --exclude flipped a blocked gate to
	// allowed. Reasons is the human-readable failure list for
	// the post-exclude state; the dashboard renders it verbatim
	// in the gate card so the operator sees why a still-blocked
	// gate is blocked. omitempty drops these on the success
	// path so existing --json consumers stay stable.
	CanApplyPreExclude   bool     `json:"can_apply_pre_exclude,omitempty"`
	GateRescuedByExclude bool     `json:"gate_rescued_by_exclude,omitempty"`
	CanApplyReasons      []string `json:"can_apply_reasons,omitempty"`
	// ADR-124: blast-radius partition. WillDeploy + Unaffected
	// together enumerate every non-deleted app in the account plus
	// the scan's proposed creates. Skipped is the operator-excluded
	// subset of WillDeploy. Removed is the destructive subset of
	// Unaffected (existing apps absent from the scan).
	WillDeploy []PlanAffectedApp `json:"will_deploy,omitempty"`
	Unaffected []PlanAffectedApp `json:"unaffected,omitempty"`
	Skipped    []PlanAffectedApp `json:"skipped,omitempty"`
	Removed    []string          `json:"removed,omitempty"`
	// PersistedExclusions (ADR-124 follow-up #3) lists every slug
	// that was folded into this scan/apply from the persisted
	// deployment_scope_exclusions table — i.e. the operator's
	// "I excluded this for the long haul" intent. The handler
	// emits one KindProjectScopeExcluded audit row per slug. Empty
	// on the common path (no persisted exclusions); the omitempty
	// keeps existing --json consumers stable.
	PersistedExclusions []string `json:"persisted_exclusions,omitempty"`
	// StalePersistedExclusions (code-review fix #2) lists every
	// slug that was carried forward from the persisted table but
	// is no longer present in the current scan (workload was
	// renamed or deleted in a future commit). Surfaced so the
	// dashboard can render a "persisted exclusion ignored"
	// badge and so the operator can run
	// `gregale deployments exclude clear --slug=...` to drop a
	// stale row before the 90-day janitor reaps it.
	StalePersistedExclusions []string `json:"stale_persisted_exclusions,omitempty"`
}

type ApplyResponse struct {
	PlanResponse
	ProjectID string             `json:"project_id"`
	Apps      []ApplyResponseApp `json:"apps"`
	Builds    []AppliedBuild     `json:"builds,omitempty"`
}

type ApplyResponseApp struct {
	Slug string `json:"slug"`
	ID   string `json:"id"`
}

type AppliedBuild struct {
	Slug         string `json:"slug"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	BuildID      string `json:"build_id,omitempty"`
	// DeploymentStatus and BuildStatus are populated by clients that wait for
	// project readiness. They are omitted in the immediate server enqueue
	// response, preserving the original apply wire shape for --no-wait users.
	DeploymentStatus string `json:"deployment_status,omitempty"`
	BuildStatus      string `json:"build_status,omitempty"`
	Error            string `json:"error,omitempty"`
}
