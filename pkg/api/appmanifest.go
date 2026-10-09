package api

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

// AppManifestPath is where imaged writes the manifest inside the app layer and
// where guest-init reads it at boot (spec §4.6, §4.8).
const AppManifestPath = "/etc/faas/app.json"

// FullRootfsMarkerPath identifies an ext4 artifact that already contains a
// complete OCI root filesystem. guest-init uses it to skip the shared-base
// overlay assembly and pivot directly into the mounted image. The regular
// two-drive builder removes this marker from customer content before
// publishing, so an image cannot opt itself into the mode.
const FullRootfsMarkerPath = "/etc/faas/.full-rootfs"

// FullRootfsMarkerValue is the authenticated marker payload written by the
// imaged builder and checked by guest-init before bypassing the shared base.
const FullRootfsMarkerValue = "gregale-full-rootfs-v1\n"

// SidecarWorkloadManifestPath is the directory where imaged stores the
// effective runtime contract for each sidecar. The sidecar name is appended
// as one validated path component and the file name is workload.json. Keeping
// the image contract in the sidecar layer makes it available on every cold
// boot and restore without sending plaintext customer command or environment
// data over the wake wire.
const SidecarWorkloadManifestPath = "/etc/faas/workloads"

// FullRootfsSidecarMountPath is the guest-only mount root for sidecar image
// drives when the main workload uses a self-contained full-rootfs artifact.
// guest-init creates one validated child directory per sidecar beneath this
// platform-owned path and runs the workload from the sidecar image's root.
const FullRootfsSidecarMountPath = "/run/faas/sidecars"

// Defaults for the guest runtime contract (spec §4.8, §4.9).
const (
	DefaultAppPort = 8080  // the :8080 contract
	DefaultAppUser = "app" // uid 1000 inside the guest
	DefaultAppUID  = 1000
)

// AfterRestoreHook is an opt-in, guest-local HTTP callback. It runs after
// entropy and clock repair and before a restored instance can be published.
// The callback must be idempotent: a failed restore may be retried.
type AfterRestoreHook struct {
	Path      string `json:"path" yaml:"path"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

// BeforeCheckpointHook runs in the source guest before a new terminal init
// snapshot. A successful response is required to publish that snapshot.
type BeforeCheckpointHook struct {
	Path      string `json:"path" yaml:"path"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

func (h *BeforeCheckpointHook) Validate() error {
	if h == nil {
		return nil
	}
	return validateLifecycleHook("before_checkpoint", h.Path, h.TimeoutMS)
}

func (h *BeforeCheckpointHook) EffectiveTimeout() time.Duration {
	if h == nil || h.TimeoutMS == 0 {
		return time.Duration(AfterRestoreHookDefaultTimeoutMS) * time.Millisecond
	}
	return time.Duration(h.TimeoutMS) * time.Millisecond
}

func (h *AfterRestoreHook) Validate() error {
	if h == nil {
		return nil
	}
	return validateLifecycleHook("after_restore", h.Path, h.TimeoutMS)
}

func validateLifecycleHook(name, path string, timeoutMS int) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "?#%") {
		return fmt.Errorf("%s.path must be an absolute path without query, fragment, or percent escapes", name)
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s.path must not contain control characters", name)
		}
	}
	if timeoutMS < 0 || timeoutMS > AfterRestoreHookMaxTimeoutMS {
		return fmt.Errorf("%s.timeout_ms must be between 0 and %d", name, AfterRestoreHookMaxTimeoutMS)
	}
	return nil
}

func (h *AfterRestoreHook) EffectiveTimeout() time.Duration {
	if h == nil || h.TimeoutMS == 0 {
		return time.Duration(AfterRestoreHookDefaultTimeoutMS) * time.Millisecond
	}
	return time.Duration(h.TimeoutMS) * time.Millisecond
}

// ExecutionMode is the customer-controlled lifecycle axis for an app
// (issue #1186 §D, ADR-137). Default is ExecutionModeRequest which
// preserves the M-1 / pre-M-2 behaviour. Runtime wiring of the four
// modes is implemented in M-2 commits 5-8; M-3 / M-4 widen the
// per-mode surface further (named-user lookup, replica rolling
// deploys, etc.).
const (
	ExecutionModeRequest = "request" // default — request-driven HTTP/WS/etc. (today's shape)
	ExecutionModeService = "service" // replicated HTTP service with desired-count
	ExecutionModeWorker  = "worker"  // long-running daemon, no public port, idle-exempt
	ExecutionModeJob     = "job"     // run-to-completion, RestartPolicy default "no"
)

// RestartPolicy governs how the supervisor restarts a stopped workload
// (issue #1186 §D.3, ADR-137 §Decision 2). Default per-mode; override
// allowed except the job+always combination which is rejected at
// Validate().
const (
	RestartPolicyNo            = "no"             // never restart
	RestartPolicyOnFailure     = "on-failure"     // restart on non-zero exit
	RestartPolicyAlways        = "always"         // restart on any exit
	RestartPolicyUnlessStopped = "unless-stopped" // restart unless explicitly stopped
)

// ServiceReplicas is the per-deployment replica scaffold (issue #1186
// §D, ADR-137 §Decision 3). M-2 lays the schema + admission; full
// rolling deploy / rollback semantics land in M-4 workstream E.
type ServiceReplicas struct {
	Min     int `json:"min" yaml:"min"`
	Max     int `json:"max" yaml:"max"`
	Desired int `json:"desired" yaml:"desired"`
}

// WorkerScaling is the queue-driven autoscaling policy for worker-mode apps.
type WorkerScaling struct {
	Min    int     `json:"min" yaml:"min"`
	Max    int     `json:"max" yaml:"max"`
	Metric string  `json:"metric,omitempty" yaml:"metric,omitempty"`
	Name   string  `json:"name,omitempty" yaml:"name,omitempty"`
	Target float64 `json:"target,omitempty" yaml:"target,omitempty"`
}

// AppManifest is the /etc/faas/app.json contract: the single handoff from the
// build/imaging side (imaged) to the guest side (guest-init). imaged writes it
// into the app layer; guest-init applies env, execs the entrypoint as the app
// user, and uses Port/Healthz for readiness. Keep this struct stable — it is a
// cross-boundary contract baked into every snapshot.
//
// M-1 (ADR-136) widened the contract additively: Healthcheck, StopSignal,
// StopGracePeriod surfaced from the OCI image-config spec; old guest-init
// ignores unknown JSON keys per encoding/json semantics. Runtime wiring of
// the new fields lands in M-2.
type AppManifest struct {
	// Entrypoint is the exec argv for the customer app. Required.
	Entrypoint []string `json:"entrypoint"`
	// Env is applied before exec. Secret values are injected at boot, not stored
	// here (spec gap G2) — never put secrets in the manifest.
	Env map[string]string `json:"env,omitempty"`
	// EnvSecrets carries sealed-secret REFs ("secret:NAME" strings); the host
	// resolves them at wake against the app_secrets table (issue #460 /
	// ADR-053 §Decision 1). Values NEVER contain plaintext — only refs.
	// guest-init does not read this field; pkg/sched/engine.go's
	// loadSealedEnvFor consumes it via the deployment row, not the manifest.
	EnvSecrets map[string]string `json:"env_secrets,omitempty"`
	// WorkingDir is the app's cwd; empty means "/".
	WorkingDir string `json:"working_dir,omitempty"`
	// Port is the readiness/serving port; 0 means DefaultAppPort.
	Port int `json:"port,omitempty"`
	// Ports preserves the OCI image's protocol-aware listener declarations.
	// Port remains the primary HTTP/readiness contract; Ports lets workloads
	// discover additional TCP or UDP listeners inside their shared netns. Named
	// TCP entries may also be selected at the public edge by the app-owned
	// `--port-<name>` hostname form.
	Ports []WorkloadPort `json:"ports,omitempty"`
	// Healthz, if set, is a GET path guest-init probes for readiness instead of a
	// bare TCP accept (spec §4.8).
	Healthz string `json:"healthz,omitempty"`
	// User is the OCI user or user:group to exec as; empty means DefaultAppUser.
	User string `json:"user,omitempty"`
	// Healthcheck mirrors the OCI HEALTHCHECK shape when populated
	// from the source image config (issue #1186 workstream A.4).
	// Runtime polling lands in M-2 (ADR-X5); M-1 surfaces the
	// field so the contract is canonical from the registry pull
	// path onward.
	Healthcheck *AppManifestHealthcheck `json:"healthcheck,omitempty"`
	// StopSignal mirrors OCI STOPSIGNAL; runtime signal-forwarding
	// lands in M-2 (ADR-X3 lifecycle contract).
	StopSignal string `json:"stop_signal,omitempty"`
	// SecretReloadSignal opts this image's workload into live secret-file
	// replacement followed by this signal. The application must handle the
	// signal, reread FAAS_SECRETS_FILE, and apply the new values itself.
	SecretReloadSignal string `json:"secret_reload_signal,omitempty"`
	// SecretReloadReadiness waits for a per-process ready marker before signaling.
	SecretReloadReadiness bool `json:"secret_reload_readiness,omitempty"`
	// StopGracePeriod mirrors OCI StopGracePeriod (the OCI image
	// spec doesn't carry it; M-2 will populate from operator
	// override or per-plan cap). Currently always zero.
	StopGracePeriod time.Duration `json:"stop_grace_period,omitempty"`
	// ExecutionMode is the customer-controlled lifecycle axis (ADR-137).
	// Empty means ExecutionModeRequest which preserves today's
	// request-driven shape. M-2 commit 6 wires Engine.StopInstance +
	// the worker/service/job dispatch.
	ExecutionMode string `json:"execution_mode,omitempty"`
	// RestartPolicy governs the supervisor's restart-on-exit decision
	// (ADR-137 §Decision 2). Empty defers to per-mode default
	// (request: on-failure, service: always, worker: always, job: no).
	RestartPolicy    string                `json:"restart_policy,omitempty"`
	AfterRestore     *AfterRestoreHook     `json:"after_restore,omitempty"`
	BeforeCheckpoint *BeforeCheckpointHook `json:"before_checkpoint,omitempty"`
	Profiling        *ProfilingConfig      `json:"profiling,omitempty"`
	Tracing          *TracingConfig        `json:"tracing,omitempty"`
	// StartupDeadlineS is the upper bound on time-to-ready. After this
	// many seconds without reaching READY the instance transitions to
	// FAILED with lifecycle_failure_reason='startup_fail' (ADR-138
	// §Decision 3). 0 means inherit per-plan default.
	StartupDeadlineS int `json:"startup_deadline_s,omitempty"`
	// MaxRetries is the upper bound on consecutive restart attempts
	// before the supervisor gives up and transitions to FAILED with
	// lifecycle_failure_reason='crash_loop' (ADR-138 §Decision 3).
	// 0 means inherit per-plan default.
	MaxRetries int `json:"max_retries,omitempty"`
	// RequestTimeoutS is the per-app customer request wall-clock budget.
	// Zero inherits the plan/type default; positive values are bounded by
	// the plan request-budget ceiling.
	RequestTimeoutS int `json:"request_timeout_s,omitempty"`
	// ServiceReplicas is the per-deployment replica scaffold (ADR-137
	// §Decision 3). Only honoured when ExecutionMode=service. M-2
	// lays the schema + admission; M-4 workstream E lands the
	// rolling deploy / rollback / digest-pinning semantics.
	ServiceReplicas *ServiceReplicas `json:"service_replicas,omitempty"`
	// WorkerReplicas is the queue- or custom-metric autoscaling policy for worker mode.
	WorkerReplicas *WorkerScaling `json:"worker_replicas,omitempty"`
	// Favicon is an optional base64-encoded favicon payload for the edge
	// /favicon.ico answer. The gateway enforces a 32 KiB maximum.
	Favicon []byte `json:"favicon,omitempty"`
	// RobotsTxt is the optional per-app robots policy served at the edge.
	// Empty means the platform default allow-all policy.
	RobotsTxt string `json:"robots_txt,omitempty"`
	// HeadWakes opts the app into waking for HEAD / instead of receiving the
	// parked-app edge answer.
	HeadWakes bool `json:"head_wakes,omitempty"`
	// CrawlerPolicy controls known monitor/crawler cold requests. Empty is
	// equivalent to wake for backwards compatibility.
	CrawlerPolicy string `json:"crawler_policy,omitempty"`
	// PreAuthRateLimit optionally throttles a source before credential lookup.
	// An absent configuration leaves the ingress path unchanged.
	PreAuthRateLimit *PreAuthRateLimitConfig `json:"pre_auth_rate_limit,omitempty"`
	// HealthPath is the monitor-facing health endpoint. Empty uses /healthz.
	HealthPath string `json:"health_path,omitempty"`
	// HealthPathWakes opts Pro/Scale apps into waking for health probes.
	HealthPathWakes bool `json:"health_path_wakes,omitempty"`
	// SessionAffinity enables best-effort cookie-based routing to the same
	// running instance. The gateway fails open when that instance is gone.
	SessionAffinity bool `json:"session_affinity,omitempty"`
	// VersionAffinityCookie names a stable, non-secret browser cookie used as
	// the rollout key when Gregale-Version-Key is absent.
	VersionAffinityCookie string `json:"version_affinity_cookie,omitempty"`
	// VersionAffinityManagedCookie issues an opaque edge-owned browser cookie
	// before the first rollout pick. It cannot be combined with a cookie source.
	VersionAffinityManagedCookie bool `json:"version_affinity_managed_cookie,omitempty"`
	// RevisionPinTTLSeconds opts into retaining replaced revisions for exact
	// client pins. Zero disables skew protection.
	RevisionPinTTLSeconds int `json:"revision_pin_ttl_seconds,omitempty"`
}

const ManagedVersionAffinityCookieName = "__Host-gregale_version"

// ManagedReleaseContextCookieName stores the immutable project release
// selected for a browser document navigation. Unlike the rollout cookie, the
// value is intentionally readable by the page so browser SDKs can pin
// cross-origin managed API calls to the graph that served the SPA.
const ManagedReleaseContextCookieName = "__Host-gregale_release"

const (
	CrawlerPolicyWake   = "wake"
	CrawlerPolicyCached = "cached"
	CrawlerPolicyBlock  = "block"
)

func (m AppManifest) EffectiveCrawlerPolicy() string {
	switch m.CrawlerPolicy {
	case CrawlerPolicyCached, CrawlerPolicyBlock:
		return m.CrawlerPolicy
	default:
		return CrawlerPolicyWake
	}
}

func (m AppManifest) ValidateCrawlerPolicy() error {
	if m.CrawlerPolicy == "" || m.CrawlerPolicy == CrawlerPolicyWake ||
		m.CrawlerPolicy == CrawlerPolicyCached || m.CrawlerPolicy == CrawlerPolicyBlock {
		return nil
	}
	return fmt.Errorf("crawler_policy must be one of wake, cached, block")
}

// PreAuthRateLimitConfig is an app-owned, per-source gateway guard. It runs
// before consumer-key lookup and JWT verification. The configured rate is
// local to each gateway replica unless an exact route opts into central
// coordination. Existing app/account limits remain fleet-wide ceilings.
type PreAuthRateLimitConfig struct {
	Mode              string              `json:"mode"` // off | observe | enforce
	RequestsPerSecond int                 `json:"requests_per_second,omitempty"`
	Burst             int                 `json:"burst,omitempty"`
	Routes            []PreAuthRouteLimit `json:"routes,omitempty"`
}

// PreAuthRouteLimit adds a separate source bucket for one public method/path.
// Matching is exact, against the decoded public URL path before edge rewrites.
type PreAuthRouteLimit struct {
	Method            string                      `json:"method"`
	Path              string                      `json:"path"`
	RequestsPerSecond int                         `json:"requests_per_second"`
	Burst             int                         `json:"burst"`
	Coordination      string                      `json:"coordination,omitempty"` // local (default) | central
	FailedResponses   *PreAuthFailedResponseLimit `json:"failed_responses,omitempty"`
	ObserveTargets    bool                        `json:"observe_targets,omitempty"`
}

// PreAuthFailedResponseLimit counts only selected application 4xx responses.
// The gateway rejects subsequent requests from the same trusted source after
// the source spends this budget; successful responses never spend it.
type PreAuthFailedResponseLimit struct {
	FailuresPerMinute int    `json:"failures_per_minute"`
	Burst             int    `json:"burst"`
	Statuses          []int  `json:"statuses,omitempty"`     // defaults to 401 and 403
	Coordination      string `json:"coordination,omitempty"` // local (default) | central
}

const (
	PreAuthRateLimitOff        = "off"
	PreAuthRateLimitObserve    = "observe"
	PreAuthRateLimitEnforce    = "enforce"
	PreAuthCoordinationLocal   = "local"
	PreAuthCoordinationCentral = "central"
)

func (c *PreAuthRateLimitConfig) Validate(plan Plan) error {
	if c == nil {
		return nil
	}
	switch c.Mode {
	case PreAuthRateLimitOff:
		return nil
	case PreAuthRateLimitObserve, PreAuthRateLimitEnforce:
	default:
		return fmt.Errorf("pre_auth_rate_limit.mode must be off, observe, or enforce")
	}
	limits, ok := LimitsFor(plan)
	if !ok {
		return fmt.Errorf("pre_auth_rate_limit: unknown plan %q", plan)
	}
	if c.RequestsPerSecond < 1 || c.RequestsPerSecond > limits.RateLimitRPS ||
		c.Burst < 1 || c.Burst > limits.RateLimitBurst {
		return fmt.Errorf("pre_auth_rate_limit requests_per_second must be 1..%d and burst must be 1..%d", limits.RateLimitRPS, limits.RateLimitBurst)
	}
	return c.ValidateRoutes()
}

// ValidateRoutes checks configured route overrides without a plan lookup. The
// gateway also calls this on persisted policies before applying plan-clamped rates.
func (c *PreAuthRateLimitConfig) ValidateRoutes() error {
	if len(c.Routes) > 16 {
		return fmt.Errorf("pre_auth_rate_limit.routes allows at most 16 entries")
	}
	for i, route := range c.Routes {
		if route.Coordination != "" && route.Coordination != PreAuthCoordinationLocal && route.Coordination != PreAuthCoordinationCentral {
			return fmt.Errorf("pre_auth_rate_limit.routes coordination must be local or central")
		}
		switch route.Method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			return fmt.Errorf("pre_auth_rate_limit.routes method %q is unsupported", route.Method)
		}
		if len(route.Path) == 0 || len(route.Path) > 256 || route.Path[0] != '/' ||
			strings.ContainsAny(route.Path, "?#%\\\r\n\t") || path.Clean(route.Path) != route.Path {
			return fmt.Errorf("pre_auth_rate_limit.routes path %q must be a canonical absolute path of at most 256 bytes", route.Path)
		}
		for _, previous := range c.Routes[:i] {
			if previous.Method == route.Method && previous.Path == route.Path {
				return fmt.Errorf("pre_auth_rate_limit.routes has duplicate %s %s", route.Method, route.Path)
			}
		}
		if route.RequestsPerSecond < 1 || route.RequestsPerSecond > c.RequestsPerSecond ||
			route.Burst < 1 || route.Burst > c.Burst {
			return fmt.Errorf("pre_auth_rate_limit.routes %s %s must not exceed the app-wide rate and burst", route.Method, route.Path)
		}
		if failed := route.FailedResponses; failed != nil {
			if failed.Coordination != "" && failed.Coordination != PreAuthCoordinationLocal && failed.Coordination != PreAuthCoordinationCentral {
				return fmt.Errorf("pre_auth_rate_limit.routes %s %s failed_responses coordination must be local or central", route.Method, route.Path)
			}
			if failed.FailuresPerMinute < 1 || failed.FailuresPerMinute > route.RequestsPerSecond*60 ||
				failed.Burst < 1 || failed.Burst > route.Burst {
				return fmt.Errorf("pre_auth_rate_limit.routes %s %s failed_responses exceeds the route rate or burst", route.Method, route.Path)
			}
			if len(failed.Statuses) > 4 {
				return fmt.Errorf("pre_auth_rate_limit.routes %s %s failed_responses allows at most four statuses", route.Method, route.Path)
			}
			for i, status := range failed.Statuses {
				if status < 400 || status > 499 || status == 429 {
					return fmt.Errorf("pre_auth_rate_limit.routes %s %s failed_responses status %d is unsupported", route.Method, route.Path, status)
				}
				for _, previous := range failed.Statuses[:i] {
					if previous == status {
						return fmt.Errorf("pre_auth_rate_limit.routes %s %s failed_responses repeats status %d", route.Method, route.Path, status)
					}
				}
			}
		}
		if route.ObserveTargets && (route.Method != "POST" || route.FailedResponses == nil || route.Coordination != PreAuthCoordinationCentral) {
			return fmt.Errorf("pre_auth_rate_limit.routes %s %s observe_targets requires POST, failed_responses, and central coordination", route.Method, route.Path)
		}
	}
	return nil
}

var versionAffinityCookieNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)

// ValidateVersionAffinityCookieName keeps the configured lookup unambiguous
// and bounded. The empty name disables cookie-derived affinity.
func ValidateVersionAffinityCookieName(name string) error {
	if name == "" {
		return nil
	}
	if !versionAffinityCookieNameRe.MatchString(name) || name == "gregale_affinity" || name == ManagedVersionAffinityCookieName || name == ManagedReleaseContextCookieName {
		return fmt.Errorf("version_affinity_cookie must be a 1-64 character cookie name (letters, digits, _, ., -) other than reserved platform cookies")
	}
	return nil
}

// WorkloadPortProtocol is the transport protocol for a workload listener.
// The closed set mirrors OCI's exposed-port grammar and keeps endpoint
// discovery explicit when TCP and UDP share a numeric port.
type WorkloadPortProtocol string

const (
	WorkloadPortTCP WorkloadPortProtocol = "tcp"
	WorkloadPortUDP WorkloadPortProtocol = "udp"
)

// WorkloadPort is one protocol-aware listener declared by an image or
// workload. Name is optional for OCI-derived entries and is used to create a
// stable endpoint environment suffix when present.
type WorkloadPort struct {
	Name     string               `json:"name,omitempty"`
	Port     int                  `json:"port"`
	Protocol WorkloadPortProtocol `json:"protocol"`
	// Internal keeps a listener off every public surface (the --port-<name>
	// selector and raw TCP listeners) while same-account services still
	// reach it at the app's private service address (ADR-576). Compose
	// `expose:` declares internal listeners.
	Internal bool `json:"internal,omitempty"`
}

var workloadPortNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// EffectiveProtocol maps the omitted protocol used by old callers to TCP.
func (p WorkloadPort) EffectiveProtocol() WorkloadPortProtocol {
	if p.Protocol == "" {
		return WorkloadPortTCP
	}
	return WorkloadPortProtocol(strings.ToLower(string(p.Protocol)))
}

// ValidateWorkloadPorts validates the bounded listener contract and rejects
// duplicate names or duplicate (protocol, port) tuples.
func ValidateWorkloadPorts(ports []WorkloadPort) error {
	if len(ports) > WorkloadPortCapMax {
		return fmt.Errorf("workload ports: %d entries exceed cap %d", len(ports), WorkloadPortCapMax)
	}
	seenNames := make(map[string]struct{}, len(ports))
	seenTuples := make(map[string]struct{}, len(ports))
	for i, p := range ports {
		if p.Name != "" && !workloadPortNameRe.MatchString(strings.ToLower(p.Name)) {
			return fmt.Errorf("workload ports[%d]: invalid name %q", i, p.Name)
		}
		protocol := p.EffectiveProtocol()
		if protocol != WorkloadPortTCP && protocol != WorkloadPortUDP {
			return fmt.Errorf("workload ports[%d]: protocol %q must be tcp or udp", i, p.Protocol)
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("workload ports[%d]: port %d is outside 1..65535", i, p.Port)
		}
		if p.Name != "" {
			name := strings.ToLower(p.Name)
			if _, exists := seenNames[name]; exists {
				return fmt.Errorf("workload ports: duplicate name %q", p.Name)
			}
			seenNames[name] = struct{}{}
		}
		key := fmt.Sprintf("%s/%d", protocol, p.Port)
		if _, exists := seenTuples[key]; exists {
			return fmt.Errorf("workload ports: duplicate %s", key)
		}
		seenTuples[key] = struct{}{}
	}
	return nil
}

// AppManifestHealthcheck is the AppManifest-level projection of the OCI
// HEALTHCHECK shape (ADR-136 §Decision 3-4). Durations are encoded as
// integer seconds in legacy manifests. ImageTiming preserves Docker image
// nanosecond durations without changing customer probe overrides.
// OCIHealthcheckTiming is image-baked timing metadata, not a deployment
// probe override. Zero values inherit defaults; positive values retain exact
// Docker nanosecond precision.
type OCIHealthcheckTiming struct {
	IntervalNS      int64 `json:"interval_ns,omitempty" yaml:"interval_ns,omitempty" toml:"interval_ns,omitempty"`
	TimeoutNS       int64 `json:"timeout_ns,omitempty" yaml:"timeout_ns,omitempty" toml:"timeout_ns,omitempty"`
	StartPeriodNS   int64 `json:"start_period_ns,omitempty" yaml:"start_period_ns,omitempty" toml:"start_period_ns,omitempty"`
	StartIntervalNS int64 `json:"start_interval_ns,omitempty" yaml:"start_interval_ns,omitempty" toml:"start_interval_ns,omitempty"`
}

func (t OCIHealthcheckTiming) Validate() error {
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"interval", t.IntervalNS}, {"timeout", t.TimeoutNS},
		{"start_period", t.StartPeriodNS}, {"start_interval", t.StartIntervalNS},
	} {
		if field.value < 0 || (field.value > 0 && field.value < int64(OCIHealthcheckMinimumDuration)) {
			return fmt.Errorf("OCI healthcheck %s must be zero or at least %s", field.name, OCIHealthcheckMinimumDuration)
		}
	}
	return nil
}

type AppManifestHealthcheck struct {
	ImageTiming *OCIHealthcheckTiming `json:"image_timing,omitempty" yaml:"image_timing,omitempty" toml:"image_timing,omitempty"`
	// Test is the argv of the check command, prefixed by "CMD",
	// "CMD-SHELL", or "NONE" per Docker semantics.
	Test []string `json:"test,omitempty" yaml:"test,omitempty" toml:"test,omitempty"`
	// IntervalS is the poll cadence after StartPeriodS elapses.
	// 0 = inherit platform default (Docker: 30s).
	IntervalS int `json:"interval_s,omitempty" yaml:"interval_s,omitempty" toml:"interval_s,omitempty"`
	// TimeoutS is the per-probe exec timeout. 0 = inherit (Docker: 30s).
	TimeoutS int `json:"timeout_s,omitempty" yaml:"timeout_s,omitempty" toml:"timeout_s,omitempty"`
	// Retries is the consecutive failure count to mark unhealthy.
	// 0 = inherit (Docker: 3).
	Retries int `json:"retries,omitempty" yaml:"retries,omitempty" toml:"retries,omitempty"`
	// StartPeriodS is the startup grace during which failures
	// don't count (Docker 17.05+).
	StartPeriodS int `json:"start_period_s,omitempty" yaml:"start_period_s,omitempty" toml:"start_period_s,omitempty"`
	// The following typed actions and Cloud Run-style timing fields are used
	// by deployment sidecar probe overrides; image-baked OCI HEALTHCHECKs keep
	// using the fields above.
	Exec             *SidecarExecProbe      `json:"exec,omitempty" yaml:"exec,omitempty" toml:"exec,omitempty"`
	HTTPGet          *SidecarHTTPGetProbe   `json:"http_get,omitempty" yaml:"http_get,omitempty" toml:"http_get,omitempty"`
	TCPSocket        *SidecarTCPSocketProbe `json:"tcp_socket,omitempty" yaml:"tcp_socket,omitempty" toml:"tcp_socket,omitempty"`
	GRPC             *SidecarGRPCProbe      `json:"grpc,omitempty" yaml:"grpc,omitempty" toml:"grpc,omitempty"`
	PeriodS          int                    `json:"period_s,omitempty" yaml:"period_s,omitempty" toml:"period_s,omitempty"`
	FailureThreshold int                    `json:"failure_threshold,omitempty" yaml:"failure_threshold,omitempty" toml:"failure_threshold,omitempty"`
	SuccessThreshold int                    `json:"success_threshold,omitempty" yaml:"success_threshold,omitempty" toml:"success_threshold,omitempty"`
	InitialDelayS    int                    `json:"initial_delay_s,omitempty" yaml:"initial_delay_s,omitempty" toml:"initial_delay_s,omitempty"`
}

// EffectivePort returns Port or the default.
func (m AppManifest) EffectivePort() int {
	if m.Port == 0 {
		return DefaultAppPort
	}
	return m.Port
}

// EffectiveUser returns User or the default.
func (m AppManifest) EffectiveUser() string {
	if m.User == "" {
		return DefaultAppUser
	}
	return m.User
}

// EffectiveWorkingDir returns WorkingDir or "/".
func (m AppManifest) EffectiveWorkingDir() string {
	if m.WorkingDir == "" {
		return "/"
	}
	return m.WorkingDir
}

// EffectiveExecutionMode returns ExecutionMode or the default
// ExecutionModeRequest. The "request" default preserves the M-1
// behaviour for existing customers (no ExecutionMode set).
func (m AppManifest) EffectiveExecutionMode() string {
	if m.ExecutionMode == "" {
		return ExecutionModeRequest
	}
	return m.ExecutionMode
}

// EffectiveRestartPolicy returns RestartPolicy or the per-mode default
// (ADR-137 §Decision 2):
//   - request → "on-failure" (today's behaviour)
//   - service → "always"
//   - worker  → "always"
//   - job     → "no"
//
// Empty input is mapped to the per-mode default so existing manifests
// that omit RestartPolicy get the right semantics for free.
func (m AppManifest) EffectiveRestartPolicy() string {
	if m.RestartPolicy != "" {
		return m.RestartPolicy
	}
	switch m.EffectiveExecutionMode() {
	case ExecutionModeJob:
		return RestartPolicyNo
	case ExecutionModeService, ExecutionModeWorker:
		return RestartPolicyAlways
	default:
		// ExecutionModeRequest (today's default) preserves
		// the M-1 behaviour: clean-exit HTTP servers that
		// call server.Shutdown(ctx) on SIGTERM stop cleanly
		// without infinite-restart loops that would otherwise
		// trigger MaxRetries → false crash_loop.
		// (ADR-137 §Decision 2.)
		return RestartPolicyOnFailure
	}
}

// Validate rejects a manifest that guest-init could not act on.
// Back-compat shim: the gross MaxAppManifest* constants act as a
// fail-closed ceiling when the calling site doesn't know the
// customer's plan (e.g. legacy test fixtures). Production paths
// call ValidatePlan(plan) so the per-plan tier tightening in M-2
// can take effect. The gross constants remain as the absolute
// ceiling (Plan Scale can never exceed them) — see ADR-138
// §Decision 4.
func (m AppManifest) Validate() error {
	return m.ValidatePlan(PlanScale)
}

// ValidateLifecyclePlan validates the lifecycle portion of an app manifest
// before an image-derived entrypoint exists. App settings are persisted on the
// app row and are merged into the image manifest later, so requiring a real
// entrypoint here would make the customer API depend on deployment order.
func (m AppManifest) ValidateLifecyclePlan(plan Plan) error {
	if err := m.Profiling.Validate(plan); err != nil {
		return err
	}
	if err := m.Tracing.Validate(plan); err != nil {
		return err
	}
	if len(m.Entrypoint) == 0 {
		m.Entrypoint = []string{"__lifecycle_validation__"}
	}
	return m.ValidatePlan(plan)
}

// ValidatePlan rejects a manifest that guest-init could not act
// on, with the per-plan tier tightening from M-2 / ADR-137+138.
// Per-plan caps:
//
//	Free    : StopGracePeriod ≤  15s, StartupDeadlineS ≤  15s, MaxRetries ≤   3
//	Hobby   : StopGracePeriod ≤  30s, StartupDeadlineS ≤  30s, MaxRetries ≤   5
//	Pro     : StopGracePeriod ≤  60s, StartupDeadlineS ≤  60s, MaxRetries ≤  10
//	Scale   : StopGracePeriod ≤ 120s, StartupDeadlineS ≤ 120s, MaxRetries ≤  20
//
// (replaces the gross 5 min / 300 s / 20-retry ceilings)
//
// In addition, the per-mode replica caps from Limits are
// enforced:
//
//	WorkerReplicasMax  =  0/1/3/10 by free/hobby/pro/scale
//	ServiceReplicasMax =  0/3/5/20 by free/hobby/pro/scale
//	JobMaxRuntimeS     =  0/300/1800/3600 by free/hobby/pro/scale
//
// Free rejects every non-request ExecutionMode (matches the
// sidecar/async posture from ADR-069 / spec §4.4). The validator
// honors m.ExecuteMode being empty (default = request per
// EffectiveExecutionMode) — the per-mode lock fires only on the
// customer's explicit choice.
func (m AppManifest) ValidatePlan(plan Plan) error {
	if len(m.Entrypoint) == 0 {
		return fmt.Errorf("app manifest: empty entrypoint")
	}
	if m.Entrypoint[0] == "" {
		return fmt.Errorf("app manifest: empty entrypoint[0]")
	}
	if err := m.AfterRestore.Validate(); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	if m.AfterRestore != nil && m.EffectiveExecutionMode() != ExecutionModeRequest && m.EffectiveExecutionMode() != ExecutionModeService {
		return fmt.Errorf("app manifest: after_restore requires request or service execution mode")
	}
	if err := m.BeforeCheckpoint.Validate(); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	if m.BeforeCheckpoint != nil && m.EffectiveExecutionMode() != ExecutionModeRequest && m.EffectiveExecutionMode() != ExecutionModeService {
		return fmt.Errorf("app manifest: before_checkpoint requires request or service execution mode")
	}
	if err := m.ValidateCrawlerPolicy(); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	if err := m.PreAuthRateLimit.Validate(plan); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	if err := ValidateVersionAffinityCookieName(m.VersionAffinityCookie); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	if m.VersionAffinityManagedCookie && m.VersionAffinityCookie != "" {
		return fmt.Errorf("app manifest: version_affinity_managed_cookie and version_affinity_cookie are mutually exclusive")
	}
	if m.RevisionPinTTLSeconds < 0 || m.RevisionPinTTLSeconds > RevisionPinMaxTTLSeconds {
		return fmt.Errorf("app manifest: revision_pin_ttl_seconds must be between 0 and %d", RevisionPinMaxTTLSeconds)
	}
	if m.Port < 0 || m.Port > 65535 {
		return fmt.Errorf("app manifest: port %d out of range", m.Port)
	}
	if m.Healthcheck != nil && m.Healthcheck.ImageTiming != nil {
		if err := m.Healthcheck.ImageTiming.Validate(); err != nil {
			return fmt.Errorf("app manifest: %w", err)
		}
	}
	if m.Healthcheck != nil && m.Healthcheck.GRPC != nil {
		return fmt.Errorf("app manifest: grpc health checks are supported only for companion probes")
	}
	if m.SecretReloadReadiness && m.SecretReloadSignal == "" {
		return fmt.Errorf("app manifest: secret_reload_readiness requires secret_reload_signal")
	}
	if m.SecretReloadSignal != "" {
		switch m.SecretReloadSignal {
		case "SIGHUP", "SIGUSR1", "SIGUSR2":
		default:
			return fmt.Errorf("app manifest: secret_reload_signal %q must be one of {SIGHUP,SIGUSR1,SIGUSR2}", m.SecretReloadSignal)
		}
		if m.SecretReloadSignal == canonicalStopSignal(m.StopSignal) {
			return fmt.Errorf("app manifest: secret_reload_signal must differ from stop_signal %q", m.StopSignal)
		}
	}
	if err := ValidateWorkloadPorts(m.Ports); err != nil {
		return fmt.Errorf("app manifest: %w", err)
	}
	limits, ok := LimitsFor(plan)
	if !ok {
		return fmt.Errorf("app manifest: unknown plan %q", plan)
	}
	// StopGracePeriod cap is the per-plan ceiling. The gross
	// MaxAppManifestStopGracePeriod remains as a fail-closed
	// absolute ceiling so a future plan expansion cannot
	// silently allow > 5 min grace across the fleet (spec §4.10
	// tail-drain budget keeps the noDoS argument honest).
	if m.StopGracePeriod < 0 {
		return fmt.Errorf("app manifest: stop_grace_period %s must be >= 0", m.StopGracePeriod)
	}
	perPlanCap := time.Duration(limits.DefaultStopGracePeriodS) * time.Second
	if perPlanCap > MaxAppManifestStopGracePeriod {
		perPlanCap = MaxAppManifestStopGracePeriod
	}
	if perPlanCap > 0 && m.StopGracePeriod > perPlanCap {
		return fmt.Errorf("app manifest: stop_grace_period %s exceeds plan %q cap %s (ADR-138 §Decision 4)", m.StopGracePeriod, plan, perPlanCap)
	}
	if m.StopGracePeriod > MaxAppManifestStopGracePeriod {
		return fmt.Errorf("app manifest: stop_grace_period %s exceeds %s absolute cap", m.StopGracePeriod, MaxAppManifestStopGracePeriod)
	}
	// ExecutionMode is closed-set; empty maps to the default via
	// EffectiveExecutionMode() and is not rejected here. The wire
	// field is omitempty so a manifest that does not mention
	// execution_mode decodes as "" and EffectiveExecutionMode()
	// returns "request" — today's behaviour, preserved.
	if m.ExecutionMode != "" {
		switch m.ExecutionMode {
		case ExecutionModeRequest, ExecutionModeService, ExecutionModeWorker, ExecutionModeJob:
			// ok
		default:
			return fmt.Errorf("app manifest: execution_mode %q must be one of {request,service,worker,job}", m.ExecutionMode)
		}
	}
	// RestartPolicy is closed-set when non-empty. Per-mode invalid
	// combinations are also caught here (job+always is a footgun —
	// see ADR-137 §Decision 2).
	if m.RestartPolicy != "" {
		switch m.RestartPolicy {
		case RestartPolicyNo, RestartPolicyOnFailure, RestartPolicyAlways, RestartPolicyUnlessStopped:
			// ok
		default:
			return fmt.Errorf("app manifest: restart_policy %q must be one of {no,on-failure,always,unless-stopped}", m.RestartPolicy)
		}
	}
	if m.EffectiveExecutionMode() == ExecutionModeJob && m.RestartPolicy == RestartPolicyAlways {
		return fmt.Errorf("app manifest: restart_policy=always is rejected for execution_mode=job (use 'no', 'on-failure', or 'unless-stopped')")
	}
	// StartupDeadlineS / MaxRetries are gross-bounded AND per-plan
	// capped per ADR-138 §Decision 5. 0 means "inherit default"
	// and is always accepted; negative is rejected. The per-plan
	// cap is Limits.DefaultStartupDeadlineS / DefaultMaxRetries
	// on the matching plan entry; the gross MaxAppManifest* cap
	// remains as a fail-closed absolute ceiling (same shape as
	// StopGracePeriod above).
	if m.StartupDeadlineS < 0 {
		return fmt.Errorf("app manifest: startup_deadline_s %d must be >= 0", m.StartupDeadlineS)
	}
	startupCap := limits.DefaultStartupDeadlineS
	if startupCap > MaxAppManifestStartupDeadlineS {
		startupCap = MaxAppManifestStartupDeadlineS
	}
	if startupCap > 0 && m.StartupDeadlineS > startupCap {
		return fmt.Errorf("app manifest: startup_deadline_s %d exceeds plan %q cap %d (ADR-138 §Decision 5)", m.StartupDeadlineS, plan, startupCap)
	}
	if m.StartupDeadlineS > MaxAppManifestStartupDeadlineS {
		return fmt.Errorf("app manifest: startup_deadline_s %d exceeds %d absolute cap", m.StartupDeadlineS, MaxAppManifestStartupDeadlineS)
	}
	if m.MaxRetries < 0 {
		return fmt.Errorf("app manifest: max_retries %d must be >= 0", m.MaxRetries)
	}
	retriesCap := limits.DefaultMaxRetries
	if retriesCap > MaxAppManifestMaxRetries {
		retriesCap = MaxAppManifestMaxRetries
	}
	if retriesCap > 0 && m.MaxRetries > retriesCap {
		return fmt.Errorf("app manifest: max_retries %d exceeds plan %q cap %d (ADR-138 §Decision 5)", m.MaxRetries, plan, retriesCap)
	}
	if m.MaxRetries > MaxAppManifestMaxRetries {
		return fmt.Errorf("app manifest: max_retries %d exceeds %d absolute cap", m.MaxRetries, MaxAppManifestMaxRetries)
	}
	// RequestTimeoutS is the per-app request wall-clock budget. Zero inherits
	// the plan/type default; positive values must stay within the plan's
	// effective request-budget ceiling.
	if m.RequestTimeoutS < 0 {
		return fmt.Errorf("app manifest: request_timeout_s %d must be >= 0", m.RequestTimeoutS)
	}
	requestTimeoutCap := int(limits.RequestBudgetMaxDuration() / time.Second)
	if requestTimeoutCap > 0 && m.RequestTimeoutS > requestTimeoutCap {
		return fmt.Errorf("app manifest: request_timeout_s %d exceeds plan %q cap %d", m.RequestTimeoutS, plan, requestTimeoutCap)
	}
	// ServiceReplicas shape: only meaningful when ExecutionMode=service.
	// Rejecting the other modes here prevents a stale replica policy from
	// silently following an app after it switches back to request/worker/job.
	if m.ServiceReplicas != nil {
		if m.EffectiveExecutionMode() != ExecutionModeService {
			return fmt.Errorf("app manifest: service_replicas requires execution_mode=service")
		}
		r := m.ServiceReplicas
		if r.Min < 0 || r.Max < 0 || r.Desired < 0 {
			return fmt.Errorf("app manifest: service_replicas values must be >= 0 (got min=%d max=%d desired=%d)", r.Min, r.Max, r.Desired)
		}
		if r.Min > r.Max {
			return fmt.Errorf("app manifest: service_replicas.min %d must be <= max %d", r.Min, r.Max)
		}
		if r.Desired < r.Min || r.Desired > r.Max {
			return fmt.Errorf("app manifest: service_replicas.desired %d must be in [min=%d, max=%d]", r.Desired, r.Min, r.Max)
		}
		// Per-plan replica cap (ADR-137 §Decision 3): a free
		// customer passing ServiceReplicas.Desired > 0 is
		// asking for paid-tier capacity the plan doesn't
		// grant. Mirrors the sidecar/async Free locks at
		// updateAppHandler — the gate is here at validate
		// time so the customer sees the cap before the
		// store is touched.
		if r.Desired > limits.ServiceReplicasMax {
			return fmt.Errorf("app manifest: service_replicas.desired %d exceeds plan %q cap %d (ADR-137 §Decision 3)", r.Desired, plan, limits.ServiceReplicasMax)
		}
		if r.Max > limits.ServiceReplicasMax {
			return fmt.Errorf("app manifest: service_replicas.max %d exceeds plan %q cap %d", r.Max, plan, limits.ServiceReplicasMax)
		}
	}
	// WorkerReplicas shape: only meaningful when ExecutionMode=worker.
	if m.WorkerReplicas != nil {
		if m.EffectiveExecutionMode() != ExecutionModeWorker {
			return fmt.Errorf("app manifest: worker_replicas requires execution_mode=worker")
		}
		r := m.WorkerReplicas
		if r.Min < 0 || r.Max <= 0 || r.Max < r.Min {
			return fmt.Errorf("app manifest: worker_replicas values invalid (got min=%d max=%d)", r.Min, r.Max)
		}
		if r.Metric == "" {
			if r.Name != "" || r.Target != 0 {
				return fmt.Errorf("app manifest: worker_replicas name and target require a metric")
			}
		} else if problem := ValidateScalingTargets("worker_replicas", []ScalingTarget{{Metric: r.Metric, Name: r.Name, Value: r.Target}}); problem != nil {
			return fmt.Errorf("app manifest: %s", problem.Detail)
		}
		if r.Max > limits.WorkerReplicasMax {
			return fmt.Errorf("app manifest: worker_replicas.max %d exceeds plan %q cap %d", r.Max, plan, limits.WorkerReplicasMax)
		}
	}
	// Per-plan execution-mode allowlist (ADR-137 §Decision 3,
	// ADR-069 precedent). Free rejects every non-request mode;
	// the zero value on Limits.WorkerReplicasMax /
	// ServiceReplicasMax / JobMaxRuntimeS is the fail-closed
	// signal for "this plan doesn't unlock this mode". Empty
	// ExecutionMode maps to "request" via
	// EffectiveExecutionMode() and passes — a manifest that
	// doesn't mention the field decodes as request-mode by
	// default, so Free works transparently.
	if em := m.ExecutionMode; em != "" {
		switch em {
		case ExecutionModeRequest:
			// always allowed
		case ExecutionModeWorker:
			if limits.WorkerReplicasMax == 0 {
				return fmt.Errorf("app manifest: execution_mode=worker is not allowed on plan %q (WorkerReplicasMax=0; upgrade to Hobby+)", plan)
			}
		case ExecutionModeService:
			if limits.ServiceReplicasMax == 0 {
				return fmt.Errorf("app manifest: execution_mode=service is not allowed on plan %q (ServiceReplicasMax=0; upgrade to Hobby+)", plan)
			}
		case ExecutionModeJob:
			if limits.JobMaxRuntimeS == 0 {
				return fmt.Errorf("app manifest: execution_mode=job is not allowed on plan %q (JobMaxRuntimeS=0; upgrade to Hobby+)", plan)
			}
		}
	}
	// EnvSecrets: each value must be a "secret:NAME" ref (ADR-053 §Decision 1).
	// The grammar is shared with pkg/api/dto.go::CreateDeploymentOverrides
	// validation; we duplicate the check here (rather than import) so the
	// manifest contract is self-contained — guest-init and imaged validate
	// without depending on the apid DTO package. The full ref-name regex lives
	// in dto.go for now; if a third caller appears, export it.
	for k, v := range m.EnvSecrets {
		if !strings.HasPrefix(v, SecretRefPrefix) {
			return fmt.Errorf("app manifest: env_secrets[%q]=%q must start with %q", k, v, SecretRefPrefix)
		}
		name := strings.TrimPrefix(v, SecretRefPrefix)
		if !SecretRefNameRe.MatchString(name) {
			return fmt.Errorf("app manifest: env_secrets[%q] ref name %q must match %s", k, name, SecretRefNameRe.String())
		}
	}
	return nil
}

func canonicalStopSignal(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "SIGHUP", "HUP", "1":
		return "SIGHUP"
	case "SIGUSR1", "USR1", "10":
		return "SIGUSR1"
	case "SIGUSR2", "USR2", "12":
		return "SIGUSR2"
	default:
		return "SIGTERM"
	}
}

// WriteManifest encodes m as canonical JSON.
func WriteManifest(w io.Writer, m AppManifest) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// ReadManifest decodes and validates a manifest (guest-init's boot path).
func ReadManifest(r io.Reader) (AppManifest, error) {
	var m AppManifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return AppManifest{}, fmt.Errorf("app manifest: decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return AppManifest{}, err
	}
	return m, nil
}

// SidecarBuildManifest returns a compatibility placeholder AppManifest that
// imaged bakes into a sidecar layer (issue #463 / ADR-069 / PR-B).
//
// The placeholder exists because pkg/api.AppManifest.Validate
// rejects an empty entrypoint, and rootfs.Builder.Build calls
// Validate on its way through. Sidecars do not have a customer
// entrypoint — guest-init reads the name-scoped workload.json
// baked into the sidecar layer to discover argv/env/port at
// runtime. The placeholder is therefore never executed:
// guest-init's per-workload supervisor execs the effective argv
// from that workload.json, not this compatibility app.json. The
// string "/bin/sidecar-placeholder" is a stable marker an operator
// can grep for if it ever surfaces in a crash log (it should not).
func SidecarBuildManifest() AppManifest {
	return AppManifest{
		Entrypoint: []string{"/bin/sidecar-placeholder"},
		Port:       DefaultAppPort,
		Healthz:    "/healthz",
		HealthPath: "/healthz",
	}
}
