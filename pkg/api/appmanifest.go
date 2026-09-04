package api

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// AppManifestPath is where imaged writes the manifest inside the app layer and
// where guest-init reads it at boot (spec §4.6, §4.8).
const AppManifestPath = "/etc/faas/app.json"

// SidecarWorkloadManifestPath is the directory where imaged stores the
// effective runtime contract for each sidecar. The sidecar name is appended
// as one validated path component and the file name is workload.json. Keeping
// the image contract in the sidecar layer makes it available on every cold
// boot and restore without sending plaintext customer command or environment
// data over the wake wire.
const SidecarWorkloadManifestPath = "/etc/faas/workloads"

// Defaults for the guest runtime contract (spec §4.8, §4.9).
const (
	DefaultAppPort = 8080  // the :8080 contract
	DefaultAppUser = "app" // uid 1000 inside the guest
	DefaultAppUID  = 1000
)

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
	Min     int `json:"min"`
	Max     int `json:"max"`
	Desired int `json:"desired"`
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
	// Healthz, if set, is a GET path guest-init probes for readiness instead of a
	// bare TCP accept (spec §4.8).
	Healthz string `json:"healthz,omitempty"`
	// User is the unix user to exec as; empty means DefaultAppUser.
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
	RestartPolicy string `json:"restart_policy,omitempty"`
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
	// ServiceReplicas is the per-deployment replica scaffold (ADR-137
	// §Decision 3). Only honoured when ExecutionMode=service. M-2
	// lays the schema + admission; M-4 workstream E lands the
	// rolling deploy / rollback / digest-pinning semantics.
	ServiceReplicas *ServiceReplicas `json:"service_replicas,omitempty"`
}

// AppManifestHealthcheck is the AppManifest-level projection of the OCI
// HEALTHCHECK shape (ADR-136 §Decision 3-4). Durations are encoded as
// integer seconds at the JSON boundary to match OCI/Docker conventions.
type AppManifestHealthcheck struct {
	// Test is the argv of the check command, prefixed by "CMD",
	// "CMD-SHELL", or "NONE" per Docker semantics.
	Test []string `json:"test"`
	// IntervalS is the poll cadence after StartPeriodS elapses.
	// 0 = inherit platform default (Docker: 30s).
	IntervalS int `json:"interval_s,omitempty"`
	// TimeoutS is the per-probe exec timeout. 0 = inherit (Docker: 30s).
	TimeoutS int `json:"timeout_s,omitempty"`
	// Retries is the consecutive failure count to mark unhealthy.
	// 0 = inherit (Docker: 3).
	Retries int `json:"retries,omitempty"`
	// StartPeriodS is the startup grace during which failures
	// don't count (Docker 17.05+).
	StartPeriodS int `json:"start_period_s,omitempty"`
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
	if m.Port < 0 || m.Port > 65535 {
		return fmt.Errorf("app manifest: port %d out of range", m.Port)
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
	// ServiceReplicas shape: only meaningful when ExecutionMode=service,
	// but Validate() accepts the field whenever present (min<=max,
	// desired in [min,max], all non-negative). M-2 commit 6 wires
	// admission; M-4 workstream E lands the rolling deploy semantics.
	if m.ServiceReplicas != nil {
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
	}
}
