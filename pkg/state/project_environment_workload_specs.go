package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectEnvironmentWorkloadSettings is the complete mutable app configuration
// owned by one environment. Identity and scheduler telemetry remain on App.
// This type is internal state, never a customer response: basic auth is sealed.
type ProjectEnvironmentWorkloadSettings struct {
	Visibility              api.AppVisibility     `json:"visibility"`
	Type                    AppType               `json:"type"`
	Runtime                 string                `json:"runtime"`
	RAMMB                   int                   `json:"ram_mb"`
	CPUMillicores           int                   `json:"cpu_millicores"`
	IdleTimeoutS            int                   `json:"idle_timeout_s"`
	MaxConcurrency          int                   `json:"max_concurrency"`
	RequestRateLimitRPS     *int                  `json:"request_rate_limit_rps"`
	RequestRateLimitBurst   *int                  `json:"request_rate_limit_burst"`
	MinInstances            int                   `json:"min_instances"`
	EgressAllowlist         []netip.Prefix        `json:"egress_allowlist"`
	EgressPorts             []int                 `json:"egress_ports"`
	StaticEgressIP          *netip.Addr           `json:"static_egress_ip"`
	PublicAuthIPAllowlist   []netip.Prefix        `json:"public_auth_ip_allowlist"`
	AutoscaleTargetRPS      int                   `json:"autoscale_target_rps"`
	AutoscaleTargetCPUPct   int                   `json:"autoscale_target_cpu_pct"`
	RootDir                 string                `json:"root_dir"`
	WorkloadClass           WorkloadClass         `json:"workload_class"`
	StreamingEnabled        bool                  `json:"streaming_enabled"`
	WebSocketEnabled        bool                  `json:"websocket_enabled"`
	RouteMetricsEnabled     bool                  `json:"route_metrics_enabled"`
	AppProtocol             string                `json:"app_protocol"`
	MaintenanceMode         bool                  `json:"maintenance_mode"`
	OnlyAllowDeclaredRoutes bool                  `json:"only_allow_declared_routes"`
	DeclaredRoutes          []DeclaredRoute       `json:"declared_routes"`
	RequireSigned           bool                  `json:"require_signed"`
	SecurityPolicy          api.AppSecurityPolicy `json:"security_policy"`
	StartCommand            string                `json:"start_command"`
	Manifest                AppManifest           `json:"manifest"`
	ScalingPolicy           *ScalingPolicy        `json:"scaling_policy"`
	RetryPolicyJSON         json.RawMessage       `json:"retry_policy_json"`
	OverflowNode            *string               `json:"overflow_node"`
	WarmSnapshotEnabled     bool                  `json:"warm_snapshot_enabled"`
	RequireAuthn            bool                  `json:"require_authn"`
	PublicAuthMode          string                `json:"public_auth_mode"`
	ConsumerAuthMode        ConsumerAuthMode      `json:"consumer_auth_mode"`
	PublicAuthBasicSealed   []byte                `json:"public_auth_basic_sealed"`
	WarmSnapshotMinRequests int                   `json:"warm_snapshot_min_requests"`
	WarmSnapshotMinMs       int                   `json:"warm_snapshot_min_ms"`
	WarmPoolSize            int                   `json:"warm_pool_size"`
	EvictionPriority        string                `json:"eviction_priority"`
	CORSDefaultEnabled      *bool                 `json:"cors_default_enabled"`
	CORSDefaultOrigins      []string              `json:"cors_default_origins"`

	// WorkPolicies is a complete environment-owned collection. Nil denotes a
	// legacy deployment; an empty collection must never inherit later policies.
	WorkPolicies *ProjectEnvironmentWorkPolicySettings `json:"work_policies,omitempty"`
	// Nil preserves legacy configuration; explicit empty never inherits queues.
	QueueBindings *ProjectEnvironmentQueueSettings `json:"queue_bindings,omitempty"`
}

// WorkloadSettingsFromApp materializes effective values so future source edits
// cannot change the configuration of a cloned environment.
func WorkloadSettingsFromApp(app App) (ProjectEnvironmentWorkloadSettings, error) {
	return cloneWorkloadSettings(ProjectEnvironmentWorkloadSettings{
		Visibility:              app.Visibility,
		Type:                    app.Type,
		Runtime:                 app.Runtime,
		RAMMB:                   app.RAMMB,
		CPUMillicores:           app.CPUMillicores,
		IdleTimeoutS:            app.IdleTimeoutS,
		MaxConcurrency:          app.MaxConcurrency,
		RequestRateLimitRPS:     app.RequestRateLimitRPS,
		RequestRateLimitBurst:   app.RequestRateLimitBurst,
		MinInstances:            app.MinInstances,
		EgressAllowlist:         app.EgressAllowlist,
		EgressPorts:             app.EgressPorts,
		StaticEgressIP:          app.StaticEgressIP,
		PublicAuthIPAllowlist:   app.PublicAuthIPAllowlist,
		AutoscaleTargetRPS:      app.AutoscaleTargetRPS,
		AutoscaleTargetCPUPct:   app.AutoscaleTargetCPUPct,
		RootDir:                 app.RootDir,
		WorkloadClass:           app.WorkloadClass,
		StreamingEnabled:        app.StreamingEnabled,
		WebSocketEnabled:        app.WebSocketEnabled,
		RouteMetricsEnabled:     app.RouteMetricsEnabled,
		AppProtocol:             app.AppProtocol,
		MaintenanceMode:         app.MaintenanceMode,
		OnlyAllowDeclaredRoutes: app.OnlyAllowDeclaredRoutes,
		DeclaredRoutes:          app.DeclaredRoutes,
		RequireSigned:           app.RequireSigned,
		SecurityPolicy:          app.SecurityPolicy,
		StartCommand:            app.StartCommand,
		Manifest:                app.Manifest,
		ScalingPolicy:           app.ScalingPolicy,
		RetryPolicyJSON:         app.RetryPolicyJSON,
		OverflowNode:            app.OverflowNode,
		WarmSnapshotEnabled:     app.WarmSnapshotEnabled,
		RequireAuthn:            app.RequireAuthn,
		PublicAuthMode:          app.PublicAuthMode,
		ConsumerAuthMode:        app.ConsumerAuthMode,
		PublicAuthBasicSealed:   app.PublicAuthBasicSealed,
		WarmSnapshotMinRequests: app.WarmSnapshotMinRequests,
		WarmSnapshotMinMs:       app.WarmSnapshotMinMs,
		WarmPoolSize:            app.WarmPoolSize,
		EvictionPriority:        app.EvictionPriority,
		CORSDefaultEnabled:      app.CORSDefaultEnabled,
		CORSDefaultOrigins:      app.CORSDefaultOrigins,
	})
}

func (settings ProjectEnvironmentWorkloadSettings) ApplyTo(app App) (App, error) {
	copy, err := cloneWorkloadSettings(settings)
	if err != nil {
		return App{}, err
	}
	app.Visibility = copy.Visibility
	app.Type = copy.Type
	app.Runtime = copy.Runtime
	app.RAMMB = copy.RAMMB
	app.CPUMillicores = copy.CPUMillicores
	app.IdleTimeoutS = copy.IdleTimeoutS
	app.MaxConcurrency = copy.MaxConcurrency
	app.RequestRateLimitRPS = copy.RequestRateLimitRPS
	app.RequestRateLimitBurst = copy.RequestRateLimitBurst
	app.MinInstances = copy.MinInstances
	app.EgressAllowlist = copy.EgressAllowlist
	app.EgressPorts = copy.EgressPorts
	app.StaticEgressIP = copy.StaticEgressIP
	app.PublicAuthIPAllowlist = copy.PublicAuthIPAllowlist
	app.AutoscaleTargetRPS = copy.AutoscaleTargetRPS
	app.AutoscaleTargetCPUPct = copy.AutoscaleTargetCPUPct
	app.RootDir = copy.RootDir
	app.WorkloadClass = copy.WorkloadClass
	app.StreamingEnabled = copy.StreamingEnabled
	app.WebSocketEnabled = copy.WebSocketEnabled
	app.RouteMetricsEnabled = copy.RouteMetricsEnabled
	app.AppProtocol = copy.AppProtocol
	app.MaintenanceMode = copy.MaintenanceMode
	app.OnlyAllowDeclaredRoutes = copy.OnlyAllowDeclaredRoutes
	app.DeclaredRoutes = copy.DeclaredRoutes
	app.RequireSigned = copy.RequireSigned
	app.SecurityPolicy = copy.SecurityPolicy
	app.StartCommand = copy.StartCommand
	app.Manifest = copy.Manifest
	app.ScalingPolicy = copy.ScalingPolicy
	app.RetryPolicyJSON = copy.RetryPolicyJSON
	app.OverflowNode = copy.OverflowNode
	app.WarmSnapshotEnabled = copy.WarmSnapshotEnabled
	app.RequireAuthn = copy.RequireAuthn
	app.PublicAuthMode = copy.PublicAuthMode
	app.ConsumerAuthMode = copy.ConsumerAuthMode
	app.PublicAuthBasicSealed = copy.PublicAuthBasicSealed
	app.WarmSnapshotMinRequests = copy.WarmSnapshotMinRequests
	app.WarmSnapshotMinMs = copy.WarmSnapshotMinMs
	app.WarmPoolSize = copy.WarmPoolSize
	app.EvictionPriority = copy.EvictionPriority
	app.CORSDefaultEnabled = copy.CORSDefaultEnabled
	app.CORSDefaultOrigins = copy.CORSDefaultOrigins
	return app, nil
}

func cloneWorkloadSettings(settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSettings, error) {
	settings, err := normalizeWorkloadQueueSettings(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	if settings.WorkPolicies != nil {
		policies, err := normalizeEnvironmentWorkPolicySettings(*settings.WorkPolicies)
		if err != nil {
			return ProjectEnvironmentWorkloadSettings{}, err
		}
		settings.WorkPolicies = &policies
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, ErrInvalidArgument
	}
	var copy ProjectEnvironmentWorkloadSettings
	if err := json.Unmarshal(raw, &copy); err != nil {
		return ProjectEnvironmentWorkloadSettings{}, ErrInvalidArgument
	}
	return copy, nil
}

func WorkloadSettingsHash(settings ProjectEnvironmentWorkloadSettings) (string, error) {
	settings, err := normalizeWorkloadQueueSettings(settings)
	if err != nil {
		return "", err
	}
	if settings.WorkPolicies != nil {
		policies, err := normalizeEnvironmentWorkPolicySettings(*settings.WorkPolicies)
		if err != nil {
			return "", err
		}
		settings.WorkPolicies = &policies
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", ErrInvalidArgument
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// MaterializeEnvironmentWorkloadSettings captures the legacy environment
// route override along with App fields when a desired head does not exist yet.
func MaterializeEnvironmentWorkloadSettings(ctx context.Context, store any, app App, environment string) (ProjectEnvironmentWorkloadSettings, error) {
	settings, err := WorkloadSettingsFromApp(app)
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	if reader, ok := store.(interface {
		GetProjectEnvironmentRoutePolicy(context.Context, string, string, string) (ProjectEnvironmentRoutePolicy, error)
	}); ok && app.ProjectID != "" {
		policy, err := reader.GetProjectEnvironmentRoutePolicy(ctx, app.AccountID, app.ID, workloadEnvironmentSlug(environment))
		if err == nil {
			settings.OnlyAllowDeclaredRoutes, settings.DeclaredRoutes = policy.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(policy.DeclaredRoutes)
		} else if !errors.Is(err, ErrNotFound) {
			return ProjectEnvironmentWorkloadSettings{}, err
		}
	}
	return settings, nil
}

func ApplyWorkloadSettingsUpdate(settings ProjectEnvironmentWorkloadSettings, params UpdateAppParams) (ProjectEnvironmentWorkloadSettings, error) {
	if params.Status != nil || params.WorkloadName != nil ||
		(params.SetRequestRateLimitRPS && params.RequestRateLimitRPS != nil && *params.RequestRateLimitRPS < 0) ||
		(params.SetRequestRateLimitBurst && params.RequestRateLimitBurst != nil && *params.RequestRateLimitBurst < 0) {
		return ProjectEnvironmentWorkloadSettings{}, ErrInvalidArgument
	}
	app, err := settings.ApplyTo(App{})
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	updated, err := WorkloadSettingsFromApp(applyAppConfigurationParams(app, params))
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	updated.WorkPolicies = settings.WorkPolicies
	return cloneWorkloadSettings(updated)
}

// UpdateEnvironmentWorkloadSettings edits the desired revision; the next
// deployment pins it. Existing releases continue using their tested revision.
func UpdateEnvironmentWorkloadSettings(ctx context.Context, store ProjectEnvironmentWorkloadSpecStore, app App, environment ProjectEnvironment, expectedRevision *int64, params UpdateAppParams) (ProjectEnvironmentWorkloadSpec, error) {
	if app.ProjectID == "" {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	current, err := store.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment.Slug, app.ID)
	if errors.Is(err, ErrNotFound) {
		current.Settings, err = MaterializeEnvironmentWorkloadSettings(ctx, store, app, environment.Slug)
	}
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	settings, err := ApplyWorkloadSettingsUpdate(current.Settings, params)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return store.PutUnprotectedProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment.ID, app.ID, current.Revision, settings)
}

type WorkloadFieldPolicy string

const (
	WorkloadFieldCopy        WorkloadFieldPolicy = "copy"
	WorkloadFieldRemap       WorkloadFieldPolicy = "remap"
	WorkloadFieldIdentity    WorkloadFieldPolicy = "identity"
	WorkloadFieldOperational WorkloadFieldPolicy = "operational"
)

// Every App field has an explicit stage policy. The coverage test rejects new
// fields until their ownership and clone behavior have been considered.
var workloadAppFieldPolicies = map[string]WorkloadFieldPolicy{
	"ID":                        WorkloadFieldIdentity,
	"AccountID":                 WorkloadFieldIdentity,
	"OrgID":                     WorkloadFieldIdentity,
	"Slug":                      WorkloadFieldIdentity,
	"Visibility":                WorkloadFieldCopy,
	"Type":                      WorkloadFieldCopy,
	"Runtime":                   WorkloadFieldCopy,
	"RAMMB":                     WorkloadFieldCopy,
	"CPUMillicores":             WorkloadFieldCopy,
	"IdleTimeoutS":              WorkloadFieldCopy,
	"MaxConcurrency":            WorkloadFieldCopy,
	"RequestRateLimitRPS":       WorkloadFieldCopy,
	"RequestRateLimitBurst":     WorkloadFieldCopy,
	"MinInstances":              WorkloadFieldCopy,
	"ScalingPolicyRevision":     WorkloadFieldOperational,
	"EgressAllowlist":           WorkloadFieldCopy,
	"EgressPorts":               WorkloadFieldCopy,
	"StaticEgressIP":            WorkloadFieldRemap,
	"StaticEgressIPSetAt":       WorkloadFieldOperational,
	"PublicAuthIPAllowlist":     WorkloadFieldCopy,
	"AutoscaleTargetRPS":        WorkloadFieldCopy,
	"AutoscaleTargetCPUPct":     WorkloadFieldCopy,
	"Status":                    WorkloadFieldOperational,
	"DeletedAt":                 WorkloadFieldOperational,
	"DeleteGraceUntil":          WorkloadFieldOperational,
	"ProjectID":                 WorkloadFieldIdentity,
	"RootDir":                   WorkloadFieldCopy,
	"WorkloadName":              WorkloadFieldIdentity,
	"WorkloadClass":             WorkloadFieldCopy,
	"StreamingEnabled":          WorkloadFieldCopy,
	"WebSocketEnabled":          WorkloadFieldCopy,
	"RouteMetricsEnabled":       WorkloadFieldCopy,
	"AppProtocol":               WorkloadFieldCopy,
	"MaintenanceMode":           WorkloadFieldCopy,
	"OnlyAllowDeclaredRoutes":   WorkloadFieldCopy,
	"DeclaredRoutes":            WorkloadFieldCopy,
	"RequireSigned":             WorkloadFieldCopy,
	"SecurityPolicy":            WorkloadFieldCopy,
	"StartCommand":              WorkloadFieldCopy,
	"Manifest":                  WorkloadFieldCopy,
	"ScalingPolicy":             WorkloadFieldCopy,
	"RetryPolicyJSON":           WorkloadFieldCopy,
	"LastScaleOutAt":            WorkloadFieldOperational,
	"LastScaleInAt":             WorkloadFieldOperational,
	"NodeID":                    WorkloadFieldOperational,
	"OverflowNode":              WorkloadFieldRemap,
	"ReassignedAt":              WorkloadFieldOperational,
	"MigratedAt":                WorkloadFieldOperational,
	"WarmSnapshotEnabled":       WorkloadFieldCopy,
	"RequireAuthn":              WorkloadFieldCopy,
	"PublicAuthMode":            WorkloadFieldCopy,
	"ConsumerAuthMode":          WorkloadFieldCopy,
	"PublicAuthBasicSealed":     WorkloadFieldCopy,
	"AuthDefaultFlippedAt":      WorkloadFieldOperational,
	"WarmSnapshotMinRequests":   WorkloadFieldCopy,
	"WarmSnapshotMinMs":         WorkloadFieldCopy,
	"WarmPoolSize":              WorkloadFieldCopy,
	"EvictionPriority":          WorkloadFieldCopy,
	"PreviewOfSlug":             WorkloadFieldIdentity,
	"PreviewPrNumber":           WorkloadFieldIdentity,
	"PreviewPrState":            WorkloadFieldOperational,
	"PreviewExpiresAt":          WorkloadFieldOperational,
	"PreviewDestroyCommentedAt": WorkloadFieldOperational,
	"CORSDefaultEnabled":        WorkloadFieldCopy,
	"CORSDefaultOrigins":        WorkloadFieldCopy,
	"CreatedAt":                 WorkloadFieldOperational,
}

func WorkloadAppFieldPolicies() map[string]WorkloadFieldPolicy {
	copy := make(map[string]WorkloadFieldPolicy, len(workloadAppFieldPolicies))
	for name, policy := range workloadAppFieldPolicies {
		copy[name] = policy
	}
	return copy
}

// A spec revision is immutable; edits move the head with compare-and-swap.
// Deployments can retain an ID to resolve the exact tested revision later.
type ProjectEnvironmentWorkloadSpec struct {
	ID              string
	AccountID       string
	ProjectID       string
	EnvironmentID   string
	EnvironmentSlug string
	AppID           string
	Revision        int64
	Hash            string
	Settings        ProjectEnvironmentWorkloadSettings
	CreatedAt       time.Time
}

type ProjectEnvironmentWorkloadSpecReader interface {
	ProjectEnvironmentWorkloadSpec(context.Context, string, string, string, string) (ProjectEnvironmentWorkloadSpec, error)
	ProjectEnvironmentWorkloadSpecByID(context.Context, string, string, string) (ProjectEnvironmentWorkloadSpec, error)
}

type ProjectEnvironmentWorkloadSpecStore interface {
	ProjectEnvironmentWorkloadSpecReader
	PutProjectEnvironmentWorkloadSpec(context.Context, string, string, string, string, int64, ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error)
	PutUnprotectedProjectEnvironmentWorkloadSpec(context.Context, string, string, string, string, int64, ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error)
}

type DeploymentWorkloadSpecReader interface {
	ProjectEnvironmentWorkloadSpecForDeployment(context.Context, string, string, string) (ProjectEnvironmentWorkloadSpec, error)
}

// AppForDeployment is the shared build, prime, wake and routing resolver. A
// pinned deployment keeps its tested settings after subsequent stage edits.
func AppForDeployment(ctx context.Context, store interface {
	AppByID(context.Context, string) (App, error)
}, deployment Deployment) (App, error) {
	app, err := store.AppByID(ctx, deployment.AppID)
	if err != nil {
		return App{}, err
	}
	return ResolveAppForDeployment(ctx, store, app, deployment)
}

func ResolveAppForDeployment(ctx context.Context, store any, app App, deployment Deployment) (App, error) {
	if app.ID != deployment.AppID {
		return App{}, ErrConflict
	}
	if reader, ok := store.(DeploymentWorkloadSpecReader); ok && app.ProjectID != "" {
		spec, err := reader.ProjectEnvironmentWorkloadSpecForDeployment(ctx, app.AccountID, app.ProjectID, deployment.ID)
		if err == nil {
			if spec.AppID != app.ID || spec.EnvironmentSlug != workloadEnvironmentSlug(deployment.Scope) {
				return App{}, ErrConflict
			}
			hash, hashErr := WorkloadSettingsHash(spec.Settings)
			if hashErr != nil || hash != spec.Hash {
				return App{}, ErrConflict
			}
			return spec.Settings.ApplyTo(app)
		}
		if !errors.Is(err, ErrNotFound) {
			return App{}, err
		}
	}
	// Deployments created before environment settings existed used App directly.
	// Adopting a newly created desired head here would change a running release
	// before a deployment was built and tested with those settings.
	return app, nil
}

func AppForInstance(ctx context.Context, store interface {
	AppByID(context.Context, string) (App, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}, instance Instance) (App, error) {
	if instance.DeploymentID == "" {
		return store.AppByID(ctx, instance.AppID)
	}
	deployment, err := store.DeploymentByID(ctx, instance.DeploymentID)
	if err != nil {
		return App{}, err
	}
	if deployment.AppID != instance.AppID {
		return App{}, ErrConflict
	}
	return AppForDeployment(ctx, store, deployment)
}

func workloadEnvironmentSlug(scope string) string {
	if scope == "" || scope == api.DefaultEnvScope {
		return "production"
	}
	return scope
}

// ResolveAppForEnvironment overlays only configuration, preserving live app
// identity, ownership and deletion guards. Legacy environments without a spec
// retain their existing App configuration during the migration.
func ResolveAppForEnvironment(ctx context.Context, store any, app App, environment string) (App, error) {
	if app.ProjectID == "" {
		return app, nil
	}
	reader, ok := store.(ProjectEnvironmentWorkloadSpecReader)
	if !ok {
		return app, nil
	}
	environment = workloadEnvironmentSlug(environment)
	spec, err := reader.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment, app.ID)
	if errors.Is(err, ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return App{}, err
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return App{}, ErrConflict
	}
	return spec.Settings.ApplyTo(app)
}
