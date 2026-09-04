package api

import (
	"encoding/json"
	"fmt"
	"io"
)

// AppManifestPath is where imaged writes the manifest inside the app layer and
// where guest-init reads it at boot (spec §4.6, §4.8).
const AppManifestPath = "/etc/faas/app.json"

// Defaults for the guest runtime contract (spec §4.8, §4.9).
const (
	DefaultAppPort = 8080  // the :8080 contract
	DefaultAppUser = "app" // uid 1000 inside the guest
	DefaultAppUID  = 1000
)

// Execution modes and restart policies are part of the app lifecycle contract
// exposed by the v1 API. Empty values preserve the request-driven defaults.
const (
	ExecutionModeRequest = "request"
	ExecutionModeService = "service"
	ExecutionModeWorker  = "worker"
	ExecutionModeJob     = "job"

	RestartPolicyNo            = "no"
	RestartPolicyOnFailure     = "on-failure"
	RestartPolicyAlways        = "always"
	RestartPolicyUnlessStopped = "unless-stopped"
)

// ServiceReplicas is the desired-count policy for service-mode deployments.
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
type AppManifest struct {
	// Entrypoint is the exec argv for the customer app. Required.
	Entrypoint []string `json:"entrypoint"`
	// Env is applied before exec. Secret values are injected at boot, not stored
	// here (spec gap G2) — never put secrets in the manifest.
	Env map[string]string `json:"env,omitempty"`
	// WorkingDir is the app's cwd; empty means "/".
	WorkingDir string `json:"working_dir,omitempty"`
	// Port is the readiness/serving port; 0 means DefaultAppPort.
	Port int `json:"port,omitempty"`
	// Healthz, if set, is a GET path guest-init probes for readiness instead of a
	// bare TCP accept (spec §4.8).
	Healthz string `json:"healthz,omitempty"`
	// User is the unix user to exec as; empty means DefaultAppUser.
	User string `json:"user,omitempty"`
	// ExecutionMode selects request, replicated service, worker, or job
	// lifecycle semantics. Empty preserves the request-driven default.
	ExecutionMode string `json:"execution_mode,omitempty"`
	// RestartPolicy controls supervisor restarts. Empty defers to the
	// execution-mode default.
	RestartPolicy string `json:"restart_policy,omitempty"`
	// StartupDeadlineS is the time-to-ready bound. Zero uses the plan default.
	StartupDeadlineS int `json:"startup_deadline_s,omitempty"`
	// MaxRetries is the consecutive restart-attempt bound. Zero uses the plan
	// default.
	MaxRetries int `json:"max_retries,omitempty"`
	// ServiceReplicas is honoured when ExecutionMode is service.
	ServiceReplicas *ServiceReplicas `json:"service_replicas,omitempty"`
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

// Validate rejects a manifest that guest-init could not act on.
func (m AppManifest) Validate() error {
	if len(m.Entrypoint) == 0 {
		return fmt.Errorf("app manifest: empty entrypoint")
	}
	if m.Entrypoint[0] == "" {
		return fmt.Errorf("app manifest: empty entrypoint[0]")
	}
	if m.Port < 0 || m.Port > 65535 {
		return fmt.Errorf("app manifest: port %d out of range", m.Port)
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
