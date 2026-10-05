package state

import (
	"bytes"
	"encoding/json"
)

// EnvironmentWorkloadDeploymentInputs retains deployment-specific intent when
// a GitOps candidate inherits an existing workload. Artifact locations, runtime
// receipts, traffic weights and lifecycle state are deliberately separate.
// Sidecar env remains sealed; this projection never decrypts customer values.
type EnvironmentWorkloadDeploymentInputs struct {
	Entrypoint      []string        `json:"override_entrypoint"`
	Cmd             []string        `json:"override_cmd"`
	Env             json.RawMessage `json:"override_env"`
	SecretRefs      json.RawMessage `json:"override_env_secrets"`
	Port            int             `json:"override_port"`
	Healthcheck     json.RawMessage `json:"override_healthcheck"`
	LivenessProbe   json.RawMessage `json:"override_liveness_probe"`
	ReadinessProbe  json.RawMessage `json:"override_readiness_probe"`
	MainDependsOn   json.RawMessage `json:"override_main_depends_on"`
	Sidecars        json.RawMessage `json:"sidecars"`
	Workflows       json.RawMessage `json:"workflows"`
	FullRootfsAuto  bool            `json:"full_rootfs_allow_auto"`
	FullRootfs      *bool           `json:"full_rootfs_override"`
	MinInstances    int             `json:"min_instances"`
	ReleaseCommand  []string        `json:"release_command"`
	ReleaseShell    bool            `json:"release_command_shell"`
	DisableCPUBoost bool            `json:"disable_startup_cpu_boost"`
	RollbackOn5xx   bool            `json:"rollback_on_5xx"`
}

func deploymentInputJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), raw...)
}

func deploymentSidecarInputs(raw json.RawMessage) json.RawMessage {
	raw = deploymentInputJSON(raw, "[]")
	var sidecars []map[string]json.RawMessage
	if json.Unmarshal(raw, &sidecars) != nil {
		return raw // the existing manifest validator reports malformed input
	}
	for _, sidecar := range sidecars {
		delete(sidecar, "secret_reload_signal") // OCI-derived artifact metadata
	}
	raw, _ = json.Marshal(sidecars)
	return deploymentInputJSON(raw, "[]")
}

func environmentWorkloadDeploymentInputs(d Deployment) EnvironmentWorkloadDeploymentInputs {
	var fullRootfs *bool
	if d.FullRootfsOverride != nil {
		value := *d.FullRootfsOverride
		fullRootfs = &value
	}
	return EnvironmentWorkloadDeploymentInputs{
		Entrypoint: append([]string{}, d.OverrideEntrypoint...), Cmd: append([]string{}, d.OverrideCmd...),
		Env: deploymentInputJSON(d.OverrideEnv, "{}"), SecretRefs: deploymentInputJSON(d.OverrideEnvSecrets, "{}"),
		Port: d.OverridePort, Healthcheck: deploymentInputJSON(d.OverrideHealthcheck, "{}"),
		LivenessProbe: deploymentInputJSON(d.OverrideLivenessProbe, "{}"), ReadinessProbe: deploymentInputJSON(d.OverrideReadinessProbe, "{}"),
		MainDependsOn: deploymentInputJSON(d.OverrideMainDependsOn, "[]"), Sidecars: deploymentSidecarInputs(d.Sidecars),
		Workflows: deploymentInputJSON(d.Workflows, "[]"), FullRootfsAuto: d.FullRootfsAllowAuto, FullRootfs: fullRootfs,
		MinInstances: d.MinInstances, ReleaseCommand: append([]string{}, d.ReleaseCommand...), ReleaseShell: d.ReleaseCommandShell,
		DisableCPUBoost: d.DisableStartupCPUBoost, RollbackOn5xx: d.RollbackOn5xx,
	}
}

func (input EnvironmentWorkloadDeploymentInputs) apply(d *Deployment) {
	// Marshal round-trip detaches all slices, JSON and nullable values from the
	// observed baseline. The candidate's columns and frozen metadata agree.
	raw, _ := json.Marshal(input)
	var copy EnvironmentWorkloadDeploymentInputs
	_ = json.Unmarshal(raw, &copy)
	d.OverrideEntrypoint, d.OverrideCmd = copy.Entrypoint, copy.Cmd
	d.OverrideEnv, d.OverrideEnvSecrets, d.OverridePort = copy.Env, copy.SecretRefs, copy.Port
	d.OverrideHealthcheck, d.OverrideLivenessProbe, d.OverrideReadinessProbe = copy.Healthcheck, copy.LivenessProbe, copy.ReadinessProbe
	d.OverrideMainDependsOn, d.Sidecars, d.Workflows = copy.MainDependsOn, copy.Sidecars, copy.Workflows
	d.FullRootfsAllowAuto, d.FullRootfsOverride, d.MinInstances = copy.FullRootfsAuto, copy.FullRootfs, copy.MinInstances
	d.ReleaseCommand, d.ReleaseCommandShell = copy.ReleaseCommand, copy.ReleaseShell
	d.DisableStartupCPUBoost, d.RollbackOn5xx = copy.DisableCPUBoost, copy.RollbackOn5xx
}
